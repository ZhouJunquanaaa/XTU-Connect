package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 本文件实现「整机分流」模式：以管理员权限运行 CLI 的 TUN 模式，
// 只给内网网段加路由（不动默认路由，不与 Clash TUN 冲突），
// 并把系统 DNS 指向本程序（127.0.0.1:53），使内网域名可解析。
// SSH / VS Code / 浏览器 / 任意程序零配置透明访问校内网。

const tunHelperTemplate = `#!/bin/sh
# XTU-Connect 整机分流模式辅助脚本（由桌面程序生成，以 root 运行）
CLI=%q
LOG=%q
FLAG=%q
PIDF=%q
rm -f "$FLAG"
"$CLI" -tun-mode -add-route -socks-bind 127.0.0.1:1080 -http-bind "" -dns-server-bind 127.0.0.1:53 >>"$LOG" 2>&1 &
P=$!
echo $P > "$PIDF"
# 等待子进程退出或用户创建停止标志文件
while kill -0 $P 2>/dev/null && [ ! -e "$FLAG" ]; do sleep 1; done
kill -TERM $P 2>/dev/null
wait $P 2>/dev/null
rm -f "$FLAG" "$PIDF"
`

// writeTunHelper 生成 root 辅助脚本并返回路径
func (m *Manager) writeTunHelper() (string, error) {
	path := filepath.Join(m.configDir(), "tun-helper.sh")
	content := fmt.Sprintf(tunHelperTemplate, m.cliBin, m.tunLogFile, m.tunFlagFile, m.tunPidFile)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// launchRoot 以管理员权限后台执行辅助脚本。
// macOS 用 osascript（GUI 密码框）；Linux 用 pkexec。
// Windows 的整机模式尚未实现（未经实测），返回不支持。
func launchRoot(script string) error {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", "-e",
			`do shell script "nohup sh `+script+` >/dev/null 2>&1 & echo started" with administrator privileges`).
			CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "User canceled") || strings.Contains(string(out), "-128") {
				return fmt.Errorf("未授予管理员权限")
			}
			return fmt.Errorf("管理员启动失败: %s", strings.TrimSpace(string(out)))
		}
		return nil
	case "linux":
		cmd := exec.Command("pkexec", "sh", "-c", "nohup sh "+script+" >/dev/null 2>&1 &")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("pkexec 启动失败: %s: %v", strings.TrimSpace(string(out)), err)
		}
		return nil
	}
	return fmt.Errorf("当前平台暂不支持整机分流，请在托盘菜单关闭整机模式使用代理分流")
}

func (m *Manager) readTunPid() int {
	data, err := os.ReadFile(m.tunPidFile)
	if err != nil {
		return 0
	}
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid)
	return pid
}

// startTun 以整机模式启动 VPN（root）
func (m *Manager) startTun() error {
	if m.cliBin == "" {
		return fmt.Errorf("找不到 xtu-connect 主程序，请把它和桌面程序放在同一目录")
	}
	if !m.HasCreds() {
		return fmt.Errorf("尚未配置校园网凭据，请先在控制面板中填写")
	}
	_ = os.MkdirAll(m.configDir(), 0o700)
	// 上次的 root 进程仍在则先停止（避免服务端单会话互踢与状态错乱）
	if m.tunInUse() {
		m.stopTun()
	}
	helper, err := m.writeTunHelper()
	if err != nil {
		return err
	}
	// 清理上次残留
	_ = os.Remove(m.tunFlagFile)
	_ = os.Remove(m.tunPidFile)
	_ = os.Truncate(m.tunLogFile, 0)

	m.mu.Lock()
	m.state = StateStarting
	m.clientIP = ""
	m.socksAddr = ""
	m.lastErr = ""
	m.startedAt = time.Now()
	m.logLines = nil
	m.mu.Unlock()

	if err := launchRoot(helper); err != nil {
		m.mu.Lock()
		m.state = StateStopped
		m.lastErr = err.Error()
		m.mu.Unlock()
		return err
	}
	go m.monitorTun()
	return nil
}

// monitorTun 轮询 root 子进程的日志文件与存活状态，驱动状态机
func (m *Manager) monitorTun() {
	// 等待 pidfile 出现（后台脚本启动需要一点时间），避免把启动窗口误判为退出
	waitDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(waitDeadline) && m.readTunPid() == 0 {
		time.Sleep(300 * time.Millisecond)
	}

	offset := int64(0)
	buf := make([]byte, 0, 4096)
	lastAlive := true
	for {
		time.Sleep(500 * time.Millisecond)

		if data, err := os.ReadFile(m.tunLogFile); err == nil && int64(len(data)) > offset {
			chunk := data[offset:]
			offset = int64(len(data))
			buf = append(buf, chunk...)
			for {
				idx := indexByte(buf, '\n')
				if idx < 0 {
					break
				}
				line := strings.TrimRight(string(buf[:idx]), "\r")
				buf = buf[idx+1:]
				m.parseLine(line)
			}
			if len(buf) > 0 {
				m.parseLine(string(buf))
				buf = buf[:0]
			}
		}

		alive := rootChildAlive(m.readTunPid())
		if lastAlive && !alive {
			m.mu.Lock()
			if m.state != StateError {
				m.setStateLocked(StateStopped)
			}
			if m.lastErr == "" && m.clientIP == "" {
				m.lastErr = "整机分流进程已退出（详见日志）"
			}
			m.mu.Unlock()
			return
		}
		lastAlive = alive
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// stopTun 通过停止标志文件让 root 辅助脚本优雅结束子进程
func (m *Manager) stopTun() {
	_ = os.MkdirAll(filepath.Dir(m.tunFlagFile), 0o700)
	if err := os.WriteFile(m.tunFlagFile, []byte("stop"), 0o644); err != nil {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !rootChildAlive(m.readTunPid()) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (m *Manager) configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "xtu-connect")
}

// tunInUse 报告整机模式进程是否仍在运行（供启动前清理判断）
func (m *Manager) tunInUse() bool {
	return rootChildAlive(m.readTunPid())
}
