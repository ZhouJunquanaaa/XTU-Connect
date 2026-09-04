package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// sysProxy 在 VPN 连接成功后，把系统「自动代理（PAC）」指向本程序内置的
// 分流脚本：仅校内域名与内网网段走本代理，其余流量 DIRECT。
// 断开或退出时恢复原状。适用于浏览器等遵循系统代理的应用，
// 不创建虚拟网卡、不改路由，与 Clash TUN 模式互不影响。
type sysProxy struct {
	mu      sync.Mutex
	pacURL  string
	state   string // on / off / conflict / unsupported
	lastErr string

	// darwin：已接管的网络服务名；windows：是否由我们写入
	appliedServices []string
	appliedWin      bool
	winBackupURL    string
	winHadBackup    bool
}

func newSysProxy(pacURL string) *sysProxy {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return &sysProxy{pacURL: pacURL, state: "unsupported"}
	}
	sp := &sysProxy{pacURL: pacURL, state: "off"}
	sp.cleanupStale() // 清理上次异常退出可能遗留的 PAC 设置
	return sp
}

func (s *sysProxy) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// lastErrText 返回 conflict 状态的原因（供面板展示）
func (s *sysProxy) lastErrText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *sysProxy) setState(state, errText string) {
	s.mu.Lock()
	s.state = state
	s.lastErr = errText
	s.mu.Unlock()
}

// Apply 启用系统 PAC 分流；若系统代理已被其他程序占用（如 Clash 系统代理
// 模式），则放弃接管并记录 conflict，避免互相覆盖
func (s *sysProxy) Apply() {
	s.mu.Lock()
	if len(s.appliedServices) > 0 || s.appliedWin { // 已启用，幂等
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if runtime.GOOS == "darwin" {
		s.applyDarwin()
	} else if runtime.GOOS == "windows" {
		s.applyWindows()
	}
}

// Restore 恢复系统代理原状（幂等）
func (s *sysProxy) Restore() {
	s.mu.Lock()
	services := append([]string(nil), s.appliedServices...)
	s.appliedServices = nil
	winApplied := s.appliedWin
	s.appliedWin = false
	backupURL, hadBackup := s.winBackupURL, s.winHadBackup
	s.mu.Unlock()

	if len(services) == 0 && !winApplied {
		return
	}

	if runtime.GOOS == "darwin" {
		for _, svc := range services {
			_ = exec.Command("networksetup", "-setautoproxystate", svc, "off").Run()
		}
	} else if runtime.GOOS == "windows" && winApplied {
		restoreWindowsAutoConfig(backupURL, hadBackup)
	}
	s.setState("off", "")
}

// ---------------- darwin ----------------

func darwinEnabledServices() []string {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil
	}
	var services []string
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 { // 首行是说明文字
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") { // * 前缀表示已禁用
			continue
		}
		services = append(services, line)
	}
	return services
}

func darwinAutoProxy(svc string) (url string, enabled bool) {
	out, err := exec.Command("networksetup", "-getautoproxyurl", svc).Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "URL:") {
			url = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
		}
		if strings.HasPrefix(line, "Enabled:") {
			enabled = strings.Contains(line, "Yes")
		}
	}
	return
}

func darwinManualProxyEnabled(svc string) bool {
	out, err := exec.Command("networksetup", "-getwebproxy", svc).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Enabled: Yes")
}

func (s *sysProxy) applyDarwin() {
	var targets, mine []string
	for _, svc := range darwinEnabledServices() {
		url, enabled := darwinAutoProxy(svc)
		switch {
		case enabled && url == s.pacURL:
			mine = append(mine, svc) // 上次遗留，视作已接管
		case enabled, darwinManualProxyEnabled(svc):
			s.setState("conflict",
				fmt.Sprintf("系统代理正被其他程序占用（%s），未自动启用浏览器分流", svc))
			return
		default:
			targets = append(targets, svc)
		}
	}
	for _, svc := range targets {
		if err := exec.Command("networksetup", "-setautoproxyurl", svc, s.pacURL).Run(); err != nil {
			s.setState("conflict", "设置系统代理失败（可能需要管理员权限）: "+err.Error())
			return
		}
		mine = append(mine, svc)
	}
	s.mu.Lock()
	s.appliedServices = mine
	s.mu.Unlock()
	s.setState("on", "")
}

// cleanupStale 清理异常退出后遗留的、指向本程序 PAC 的系统设置
func (s *sysProxy) cleanupStale() {
	if runtime.GOOS != "darwin" {
		return // windows 的 AutoConfigURL 在新实例 Apply 时会被备份/覆盖，无需处理
	}
	for _, svc := range darwinEnabledServices() {
		if url, enabled := darwinAutoProxy(svc); enabled && url == s.pacURL {
			_ = exec.Command("networksetup", "-setautoproxystate", svc, "off").Run()
		}
	}
}

// ---------------- windows ----------------

func (s *sysProxy) applyWindows() {
	existing, existed, enabled, err := readWindowsAutoConfig()
	if err != nil {
		s.setState("conflict", "读取系统代理设置失败: "+err.Error())
		return
	}
	if enabled && existing != s.pacURL {
		s.setState("conflict", "系统代理正被其他程序占用，未自动启用浏览器分流")
		return
	}
	if proxyEnable, err := readWindowsProxyEnable(); err == nil && proxyEnable {
		s.setState("conflict", "系统代理正被其他程序占用（手动代理），未自动启用浏览器分流")
		return
	}
	if err := writeWindowsAutoConfig(s.pacURL); err != nil {
		s.setState("conflict", "设置系统代理失败: "+err.Error())
		return
	}
	s.mu.Lock()
	s.appliedWin = true
	s.winBackupURL, s.winHadBackup = existing, existed
	s.mu.Unlock()
	s.setState("on", "")
}
