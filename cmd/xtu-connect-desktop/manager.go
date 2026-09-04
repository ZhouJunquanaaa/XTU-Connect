package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"xtu-connect/configs"
)

type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateError    State = "error"
)

func (s State) Text() string {
	switch s {
	case StateRunning:
		return "已连接"
	case StateStarting:
		return "连接中…"
	case StateError:
		return "连接失败"
	default:
		return "未连接"
	}
}

// Status 是控制面板与托盘共享的状态快照
type Status struct {
	State     State  `json:"state"`
	StateText string `json:"state_text"`
	ClientIP  string `json:"client_ip,omitempty"`
	SocksAddr string `json:"socks_addr,omitempty"`
	LastError string `json:"last_error,omitempty"`
	StartedAt int64  `json:"started_at,omitempty"`
	HasCreds  bool   `json:"has_credentials"`
	CLIBinary string `json:"cli_binary"`
}

const maxLogLines = 500

// Manager 管理 xtu-connect CLI 子进程的完整生命周期
type Manager struct {
	mu        sync.Mutex
	actionMu  sync.Mutex // 串行化 Start/Stop/Restart，防止并发操作互相踩踏
	cmd       *exec.Cmd
	done      chan struct{}
	state     State
	clientIP  string
	socksAddr string
	lastErr   string
	startedAt time.Time
	logLines  []string
	logPath   string
	logFile   *os.File
	cliBin    string
}

func NewManager() *Manager {
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".config", "xtu-connect", "desktop.log")
	return &Manager{
		state:   StateStopped,
		logPath: logPath,
		cliBin:  findCLIBinary(),
	}
}

func findCLIBinary() string {
	name := "xtu-connect"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if isExecutable(candidate) {
			return candidate
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, name)
		if isExecutable(candidate) {
			return candidate
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// Start 启动 VPN（与其他生命周期操作互斥）
func (m *Manager) Start() error {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	return m.start()
}

func (m *Manager) start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state == StateStarting || m.state == StateRunning {
		return nil
	}
	if m.cliBin == "" {
		return fmt.Errorf("找不到 xtu-connect 主程序，请把它和桌面程序放在同一目录")
	}
	if !configs.Exists() {
		return fmt.Errorf("尚未配置校园网凭据，请先在控制面板中填写")
	}

	_ = os.MkdirAll(filepath.Dir(m.logPath), 0o700)
	logFile, err := os.OpenFile(m.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	cmd := exec.Command(m.cliBin)
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		logFile.Close()
		return err
	}
	cmd.Stdout = pipeWriter
	cmd.Stderr = pipeWriter // 合并到同一管道
	if err := cmd.Start(); err != nil {
		pipeReader.Close()
		pipeWriter.Close()
		logFile.Close()
		return fmt.Errorf("启动 xtu-connect 失败: %w", err)
	}
	pipeWriter.Close() // 父进程持有读端即可，写端交给子进程
	debugLog("child started: pid=%d bin=%s", cmd.Process.Pid, m.cliBin)

	m.cmd = cmd
	m.done = make(chan struct{})
	m.state = StateStarting
	m.clientIP = ""
	m.socksAddr = ""
	m.lastErr = ""
	m.startedAt = time.Now()
	m.logFile = logFile
	m.logLines = nil
	fmt.Fprintf(logFile, "\n===== %s 由桌面程序启动 =====\n", time.Now().Format("2006-01-02 15:04:05"))

	go m.pipeLogs(pipeReader, logFile)
	go func() {
		err := cmd.Wait()
		pipeReader.Close()
		debugLog("child wait returned: err=%v pid=%d", err, cmd.Process.Pid)
		m.mu.Lock()
		if m.state != StateError {
			m.state = StateStopped
		}
		if err != nil && m.lastErr == "" {
			m.lastErr = fmt.Sprintf("进程退出: %v", err)
		}
		if m.logFile != nil {
			m.logFile.Close()
			m.logFile = nil
		}
		m.mu.Unlock()
		close(m.done)
	}()
	return nil
}

// debugLog 写 GUI 自身的调试轨迹（独立于子进程日志）
func debugLog(format string, args ...interface{}) {
	home, _ := os.UserHomeDir()
	f, err := os.OpenFile(filepath.Join(home, ".config", "xtu-connect", "gui-debug.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("2006-01-02 15:04:05.000")+" "+fmt.Sprintf(format, args...)+"\n")
}

var (
	reClientIP = regexp.MustCompile(`Client IP: (\d+\.\d+\.\d+\.\d+)`)
	reSocks    = regexp.MustCompile(`SOCKS5 server listening on (\S+)`)
)

// pipeLogs 把子进程输出写入日志文件与内存缓冲，并解析状态跃迁
func (m *Manager) pipeLogs(reader io.Reader, logFile *os.File) {
	writer := bufio.NewWriter(logFile)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		writer.WriteString(line + "\n")
		writer.Flush()

		m.mu.Lock()
		m.logLines = append(m.logLines, line)
		if len(m.logLines) > maxLogLines {
			m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
		}
		switch {
		case strings.Contains(line, "Login failed"):
			m.state = StateError
			m.lastErr = "登录失败：请检查学号密码（也可能被其他设备的登录挤下线）"
		case strings.Contains(line, "VPN client setup error"):
			m.state = StateError
			if m.lastErr == "" {
				m.lastErr = line
			}
		case strings.Contains(line, "SOCKS5 server listening on"):
			if match := reSocks.FindStringSubmatch(line); match != nil {
				m.socksAddr = match[1]
				m.state = StateRunning
			}
		case strings.Contains(line, "Client IP:"):
			if match := reClientIP.FindStringSubmatch(line); match != nil {
				m.clientIP = match[1]
			}
		}
		m.mu.Unlock()
	}
}

// Stop 优雅停止子进程（SIGTERM，Windows 下直接 Kill），超时后强杀；幂等可重复调用
func (m *Manager) Stop() {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	m.stop()
}

func (m *Manager) stop() {
	m.mu.Lock()
	cmd, done := m.cmd, m.done
	m.cmd = nil
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}

	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func (m *Manager) Restart() {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()
	m.stop()
	// 给服务端时间释放旧会话（单会话策略：同账号同时只允许一个在线客户端）
	time.Sleep(2 * time.Second)
	_ = m.start()
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{
		State:     m.state,
		ClientIP:  m.clientIP,
		SocksAddr: m.socksAddr,
		LastError: m.lastErr,
		HasCreds:  configs.Exists(),
		CLIBinary: m.cliBin,
	}
	s.StateText = s.State.Text()
	if !m.startedAt.IsZero() && (m.state == StateRunning || m.state == StateStarting) {
		s.StartedAt = m.startedAt.Unix()
	}
	return s
}

func (m *Manager) LogTail(n int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n <= 0 || n > maxLogLines {
		n = 200
	}
	if len(m.logLines) <= n {
		out := make([]string, len(m.logLines))
		copy(out, m.logLines)
		return out
	}
	out := make([]string, n)
	copy(out, m.logLines[len(m.logLines)-n:])
	return out
}

func (m *Manager) LogPath() string { return m.logPath }

// Running 报告 VPN 是否处于连接中/已连接状态
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state == StateRunning || m.state == StateStarting
}
