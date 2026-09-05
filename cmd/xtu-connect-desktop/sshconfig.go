package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// SSH 代理规则以「托管块」形式写入 ~/.ssh/config：
// 连接成功后自动写入，断开/退出时自动移除，用户其余内容原样保留。
// 仅 darwin 启用：macOS 自带的 BSD netcat 支持 -X 5 -x 语法；
// Linux 的 netcat 实现不统一（openbsd/ncat 参数不同）、Windows 无 nc，
// 这些平台保持 unsupported，由用户手动配置。
const (
	sshManagedBegin = "# >>> XTU-Connect managed (auto-added while VPN connected, do not edit) >>>"
	sshManagedEnd   = "# <<< XTU-Connect managed <<<"
	sshManagedBody  = `# 内网 SSH 走本地 SOCKS5 代理；VPN 断开后本块自动移除
Match host "172.16.*,172.24.*,172.25.*,10.*"
    ProxyCommand nc -X 5 -x 127.0.0.1:1080 %h %p`
)

// sshConfig 管理 ~/.ssh/config 中的托管块
type sshConfig struct {
	mu      sync.Mutex
	path    string // 为空表示当前平台不支持
	applied bool
}

func newSSHConfig() *sshConfig {
	s := &sshConfig{}
	if runtime.GOOS != "darwin" {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return s
	}
	s.path = filepath.Join(home, ".ssh", "config")
	s.Remove() // 清理上次异常退出遗留的托管块
	return s
}

// State 返回 on / off / unsupported
func (s *sshConfig) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.path == "":
		return "unsupported"
	case s.applied:
		return "on"
	default:
		return "off"
	}
}

// Apply 写入（或按标记更新）托管块，幂等
func (s *sshConfig) Apply() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" || s.applied {
		return
	}
	content, err := s.read()
	if err != nil {
		debugLog("ssh-config: read failed: %v", err)
		return
	}
	updated := ensureManagedBlock(content)
	if updated == content {
		s.applied = true
		return
	}
	if err := s.write(updated); err != nil {
		debugLog("ssh-config: write failed: %v", err)
		return
	}
	s.applied = true
	debugLog("ssh-config: managed block written to %s", s.path)
}

// Remove 移除托管块，幂等
func (s *sshConfig) Remove() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return
	}
	content, err := s.read()
	if err != nil {
		return // 文件不存在等：无需处理
	}
	updated := removeManagedBlock(content)
	if updated == content {
		s.applied = false
		return
	}
	if err := s.write(updated); err != nil {
		debugLog("ssh-config: write failed: %v", err)
		return
	}
	s.applied = false
	debugLog("ssh-config: managed block removed from %s", s.path)
}

// read 读取配置文件；文件不存在时返回空内容而非错误
func (s *sshConfig) read() (string, error) {
	if _, err := os.Stat(s.path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// write 原子写入：同目录临时文件 + rename，保留原有权限，新文件 0600
func (s *sshConfig) write(content string) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(s.path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".ssh-config-xtu-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // rename 成功后此调用无效果
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

// ensureManagedBlock 返回写入托管块后的完整文件内容：
// 块不存在则追加；已存在但内容有出入则按标记原位更新（用户其余内容不动）
func ensureManagedBlock(content string) string {
	begin := strings.Index(content, sshManagedBegin)
	if begin < 0 {
		return appendManagedBlock(content)
	}
	relEnd := strings.Index(content[begin:], sshManagedEnd)
	if relEnd < 0 {
		// 结束标记丢失（曾被意外截断）：丢弃残留后整体重写
		content = strings.TrimRight(content[:begin], "\n")
		if content != "" {
			content += "\n"
		}
		return appendManagedBlock(content)
	}
	absEnd := begin + relEnd + len(sshManagedEnd)
	if absEnd < len(content) && content[absEnd] == '\n' {
		absEnd++
	}
	return content[:begin] + sshManagedBegin + "\n" + sshManagedBody + "\n" + sshManagedEnd + content[absEnd:]
}

func appendManagedBlock(content string) string {
	content = strings.TrimRight(content, "\n")
	if content != "" {
		content += "\n"
	}
	return content + "\n" + sshManagedBegin + "\n" + sshManagedBody + "\n" + sshManagedEnd + "\n"
}

// removeManagedBlock 返回移除托管块后的完整文件内容；无托管块时原样返回
func removeManagedBlock(content string) string {
	begin := strings.Index(content, sshManagedBegin)
	if begin < 0 {
		return content
	}
	end := len(content)
	if relEnd := strings.Index(content[begin:], sshManagedEnd); relEnd >= 0 {
		end = begin + relEnd + len(sshManagedEnd)
		if end < len(content) && content[end] == '\n' {
			end++
		}
	}
	// 顺带吞掉块前的一个空行，保持文件整洁
	if strings.HasSuffix(content[:begin], "\n\n") {
		begin--
	}
	return strings.TrimRight(content[:begin]+content[end:], "\n") + "\n"
}
