package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// 本地代理端口（与 configs.Default 的 socks/http 绑定保持一致）
const (
	localHTTPProxy  = "127.0.0.1:1081"
	localSocksProxy = "127.0.0.1:1080"
)

// chainProxy 描述接管系统代理前，系统里已有的手动代理（如 Clash「系统代理」
// 模式写入的 127.0.0.1:7890）。PAC 将未命中内网规则的流量链给它，实现
// 「XTU-Connect 管校内、原代理管外网」的分层分流；为空表示直连。
type chainProxy struct {
	kind string // http / socks
	addr string // host:port
}

func (c chainProxy) pacReturn() string {
	if c.kind == "socks" {
		return "SOCKS5 " + c.addr
	}
	return "PROXY " + c.addr
}

// sysProxy 在 VPN 连接成功后，把系统「自动代理（PAC）」指向本程序的分流脚本：
// 校内域名与内网网段走本地代理，其余流量按需链到原有系统代理或直连。
//
// 与其他代理软件的共存策略：
//   - Clash 等使用 TUN 模式：不占用任何系统设置，PAC 未命中的 DIRECT 流量
//     由 TUN 照常接管，互不影响
//   - Clash 等使用「系统代理」模式（手动代理）：接管前读出其代理地址写进 PAC，
//     未命中流量继续走原代理（链式分流）；手动代理设置本身不动，断开时仅
//     撤销我们的 PAC，原代理原样保留
//
// 唯一无法共存的场景是其他程序已设置「自动代理 PAC URL」——PAC 无法嵌套 PAC，
// 此时保持 conflict 状态不接管，避免静默破坏对方的分流。
type sysProxy struct {
	mu      sync.Mutex
	pacURL  string
	script  string // 面板 /proxy.pac 提供的当前脚本（按链式目标动态生成）
	chain   chainProxy
	state   string // on / off / conflict / unsupported
	lastErr string

	applyMu sync.Mutex // 串行化 Apply/Restore，防止重连时并发执行互相覆盖

	// darwin：已接管的网络服务名；windows：是否由我们写入
	appliedServices []string
	appliedWin      bool
	winBackupURL    string
	winHadBackup    bool
}

func newSysProxy() *sysProxy {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return &sysProxy{state: "unsupported"}
	}
	sp := &sysProxy{state: "off"}
	sp.setScriptLocked(chainProxy{})
	return sp
}

// SetPacURL 在面板服务起来后注入 PAC 地址，并清理上次异常退出遗留的设置
func (s *sysProxy) SetPacURL(url string) {
	s.mu.Lock()
	s.pacURL = url
	s.mu.Unlock()
	s.cleanupStale()
}

// PacScript 返回当前 PAC 脚本（面板 /proxy.pac 端点使用）
func (s *sysProxy) PacScript() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.script
}

// ChainText 返回链式接管的描述（供面板展示，未链到任何代理时为空）
func (s *sysProxy) ChainText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chain.addr == "" {
		return ""
	}
	return s.chain.addr
}

// parseWindowsProxyServer 把 Windows 注册表 ProxyServer 值解析成 PAC 可用的
// 链式代理。依次偏好 http → https → socks；整体式的 "host:port" 按代理软件
// 的混合端口处理（http 代理即兼容）。
func parseWindowsProxyServer(server string) chainProxy {
	server = strings.TrimSpace(strings.Trim(strings.TrimSpace(server), `"`))
	if server == "" {
		return chainProxy{}
	}
	if !strings.Contains(server, "=") {
		return chainProxy{kind: "http", addr: server}
	}
	var byScheme [3]chainProxy
	for _, part := range strings.Split(server, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 || kv[1] == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "http":
			if byScheme[0].addr == "" {
				byScheme[0] = chainProxy{kind: "http", addr: kv[1]}
			}
		case "https":
			if byScheme[1].addr == "" {
				byScheme[1] = chainProxy{kind: "http", addr: kv[1]}
			}
		case "socks":
			if byScheme[2].addr == "" {
				byScheme[2] = chainProxy{kind: "socks", addr: kv[1]}
			}
		}
	}
	for _, c := range byScheme {
		if c.addr != "" {
			return c
		}
	}
	return chainProxy{}
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

// setScriptLocked 按链式目标重建 PAC 脚本
func (s *sysProxy) setScriptLocked(chain chainProxy) {
	s.chain = chain
	vpn := fmt.Sprintf("PROXY %s; SOCKS5 %s", localHTTPProxy, localSocksProxy)
	fallback := "DIRECT"
	if chain.addr != "" {
		fallback = chain.pacReturn() + "; DIRECT"
	}
	s.script = fmt.Sprintf(`// XTU-Connect automatic proxy routing
// 校内域名与内网网段走校园网 VPN，其余流量 %s
function FindProxyForURL(url, host) {
	host = host.toLowerCase();
	if (shExpMatch(host, "*.xtu.edu.cn") || host === "xtu.edu.cn")
		return "%s";
	if (isPlainHostName(host)) return "DIRECT";
	if (isInNet(host, "10.0.0.0", "255.0.0.0") ||
		isInNet(host, "172.16.0.0", "255.240.0.0"))
		return "%s";
	return "%s";
}
`, fallback, vpn, vpn, fallback)
}

// Apply 启用系统 PAC 分流（幂等）。若检测到其他程序的手动系统代理，
// 将其链入 PAC 后接管；若已被其他 PAC URL 占用则放弃并记录 conflict。
func (s *sysProxy) Apply() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

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

// Restore 恢复系统代理原状（幂等）。只撤销我们自己写入的 PAC，
// 不碰其他程序的手动代理设置。
func (s *sysProxy) Restore() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

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
	s.mu.Lock()
	s.setScriptLocked(chainProxy{})
	s.mu.Unlock()
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

// darwinManualProxy 读出某网络服务上已启用的手动代理（Web → 安全 Web → SOCKS
// 依次探测，取第一个启用的），供 PAC 链式分流
func darwinManualProxy(svc string) (chainProxy, bool) {
	for _, probe := range []struct {
		command string
		kind    string
	}{
		{"-getwebproxy", "http"},
		{"-getsecurewebproxy", "http"},
		{"-getsocksfirewallproxy", "socks"},
	} {
		out, err := exec.Command("networksetup", probe.command, svc).Output()
		if err != nil {
			continue
		}
		if !strings.Contains(string(out), "Enabled: Yes") {
			continue
		}
		server := darwinProxyField(string(out), "Server")
		port := darwinProxyField(string(out), "Port")
		if server == "" || port == "" || port == "0" {
			continue
		}
		return chainProxy{kind: probe.kind, addr: server + ":" + port}, true
	}
	return chainProxy{}, false
}

func darwinProxyField(out, field string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, field+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, field+":"))
		}
	}
	return ""
}

func (s *sysProxy) applyDarwin() {
	services := darwinEnabledServices()
	if len(services) == 0 {
		s.setState("conflict", "未找到可用的网络服务，无法设置系统代理")
		return
	}

	var targets, mine []string
	chain := chainProxy{}
	for _, svc := range services {
		url, enabled := darwinAutoProxy(svc)
		switch {
		case enabled && url == s.pacURL:
			mine = append(mine, svc) // 上次遗留，视作已接管
		case enabled:
			s.setState("conflict",
				fmt.Sprintf("系统「自动代理」已被其他程序设置为 %s（PAC 无法嵌套接管，请在对方软件中改用手动系统代理）", url))
			return
		default:
			if chain.addr == "" {
				if c, ok := darwinManualProxy(svc); ok {
					chain = c // 记录原有手动代理，PAC 未命中的流量链给它
				}
			}
			targets = append(targets, svc)
		}
	}

	// 先更新 PAC 内容再写系统设置，保证浏览器拉到的脚本已含链式目标
	s.mu.Lock()
	s.setScriptLocked(chain)
	s.mu.Unlock()

	for _, svc := range targets {
		if err := exec.Command("networksetup", "-setautoproxyurl", svc, s.pacURL).Run(); err != nil {
			s.mu.Lock()
			s.appliedServices = mine // 记录已写入的，便于还原
			s.mu.Unlock()
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
	s.mu.Lock()
	pacURL := s.pacURL
	s.mu.Unlock()
	if pacURL == "" {
		return
	}
	if runtime.GOOS == "darwin" {
		for _, svc := range darwinEnabledServices() {
			if url, enabled := darwinAutoProxy(svc); enabled && url == pacURL {
				_ = exec.Command("networksetup", "-setautoproxystate", svc, "off").Run()
			}
		}
		return
	}
	if runtime.GOOS == "windows" {
		if existing, _, enabled, err := readWindowsAutoConfig(); err == nil && enabled && existing == pacURL {
			restoreWindowsAutoConfig("", false)
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
		s.setState("conflict", "系统「自动代理」已被其他程序占用（PAC 无法嵌套接管）")
		return
	}

	// 已启用的手动代理（如 Clash 系统代理模式）链入 PAC，互不覆盖
	chain := chainProxy{}
	if proxyEnable, proxyServer, err := readWindowsManualProxy(); err == nil && proxyEnable {
		chain = parseWindowsProxyServer(proxyServer)
	}

	s.mu.Lock()
	s.setScriptLocked(chain)
	s.mu.Unlock()

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
