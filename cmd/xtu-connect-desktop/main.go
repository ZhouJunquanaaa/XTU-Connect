package main

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"fyne.io/systray"
	"xtu-connect/configs"
)

//go:embed icon.png
var iconBytes []byte

var desktopVersion = "0.3.0"

func main() {
	// 单实例保护：已有实例在运行时，唤起它的控制面板并退出，
	// 避免多个托盘/面板并存导致状态互相矛盾、以及服务端单会话互踢
	if existing := findExistingPanel(); existing != "" {
		fmt.Println("XTU-Connect 已在运行，打开控制面板:", existing)
		openURL(existing)
		os.Exit(0)
	}

	manager := NewManager()

	panelURL, err := startPanelServer(manager)
	if err != nil {
		fmt.Println("控制面板启动失败:", err)
	}

	// 连接成功后自动接管系统代理（PAC 分流）：浏览器零配置直访校内网
	var proxy *sysProxy
	if panelURL != "" {
		proxy = newSysProxy(panelURL + "/proxy.pac")
		manager.SysProxy = proxy
	}

	// 终端信号也走优雅退出：先停子进程再退托盘，不依赖 systray 的 onExit 时序
	// SIGHUP 覆盖终端关闭/父进程退出的场景，避免遗留子进程与系统代理设置
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGHUP, os.Interrupt)
	go func() {
		<-sigCh
		quitGracefully(manager, proxy)
	}()

	systray.Run(func() { onReady(manager, panelURL, proxy) }, func() {
		// 兜底：systray 退出回调（幂等，同步执行确保来得及）
		manager.Stop()
		if proxy != nil {
			proxy.Restore()
		}
	})
}

// quitGracefully 同步地：断开 VPN → 恢复系统代理 → 结束托盘循环。
// 恢复必须同步完成，否则进程可能在系统命令执行完前退出，遗留 PAC 设置
func quitGracefully(manager *Manager, proxy *sysProxy) {
	go func() {
		manager.Stop()
		if proxy != nil {
			proxy.Restore()
		}
		systray.Quit()
	}()
}

// findExistingPanel 探测本机是否已有 XTU-Connect 桌面实例（扫描面板端口段）
func findExistingPanel() string {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for port := 58081; port <= 58090; port++ {
		url := fmt.Sprintf("http://127.0.0.1:%d/api/status", port)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"state"`) {
			return fmt.Sprintf("http://127.0.0.1:%d", port)
		}
	}
	return ""
}

func onReady(m *Manager, panelURL string, proxy *sysProxy) {
	systray.SetIcon(iconBytes)
	systray.SetTitle("XTU")
	systray.SetTooltip("XTU-Connect 湘潭大学校园网")
	systray.SetTemplateIcon(iconBytes, iconBytes)

	mStatus := systray.AddMenuItem("状态：加载中", "当前连接状态")
	mStatus.Disable()
	mToggle := systray.AddMenuItem("连接", "连接校园网 VPN")
	mRestart := systray.AddMenuItem("重新连接", "断开并重新连接")
	mAutoProxy := systray.AddMenuItem("浏览器直连分流（系统代理）",
		"连接后自动配置系统 PAC，浏览器无需任何设置即可访问校内网；断开时自动还原")
	if proxy != nil && proxy.State() != "unsupported" {
		mAutoProxy.Check()
	} else {
		mAutoProxy.Uncheck()
		mAutoProxy.Disable()
	}
	systray.AddSeparator()
	mPanel := systray.AddMenuItem("控制面板…", "在浏览器中打开控制面板")
	mLog := systray.AddMenuItem("查看日志…", "打开日志文件")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "断开并退出 XTU-Connect")

	// 启动即自动连接；未配置凭据则打开控制面板引导填写
	go func() {
		time.Sleep(500 * time.Millisecond)
		if configs.Exists() {
			if err := m.Start(); err != nil {
				fmt.Println("自动连接失败:", err)
			}
		} else if panelURL != "" {
			openURL(panelURL)
		}
	}()

	go func() {
		for {
			st := m.Status()
			systray.SetTooltip("XTU-Connect（" + st.StateText + "）")
			title := "状态：" + st.StateText
			if st.ClientIP != "" {
				title += " " + st.ClientIP
			}
			mStatus.SetTitle(title)
			if st.State == StateRunning || st.State == StateStarting {
				mToggle.SetTitle("断开")
			} else {
				mToggle.SetTitle("连接")
			}
			time.Sleep(2 * time.Second)
		}
	}()

	go func() {
		for {
			select {
			case <-mToggle.ClickedCh:
				if m.Running() {
					m.Stop()
				} else if err := m.Start(); err != nil {
					fmt.Println(err)
					openURL(panelURL)
				}
			case <-mRestart.ClickedCh:
				m.Restart()
			case <-mAutoProxy.ClickedCh:
				if mAutoProxy.Checked() {
					mAutoProxy.Uncheck()
					if proxy != nil {
						proxy.Restore()
					}
				} else {
					mAutoProxy.Check()
					if proxy != nil && m.Running() {
						proxy.Apply()
					}
				}
			case <-mPanel.ClickedCh:
				if panelURL != "" {
					openURL(panelURL)
				}
			case <-mLog.ClickedCh:
				openFile(m.LogPath())
			case <-mQuit.ClickedCh:
				quitGracefully(m, proxy)
				return
			}
		}
	}()
}

func openURL(url string) {
	if url == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}

func openFile(path string) {
	if path == "" {
		return
	}
	// 日志文件不存在时先创建，避免打开失败
	createEmpty(path)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-t", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", "notepad", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	cmd.Start()
}

func createEmpty(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		f.Close()
	}
}
