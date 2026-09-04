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

var desktopVersion = "0.2.1"

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

	// 终端信号也走优雅退出：先停子进程再退托盘，不依赖 systray 的 onExit 时序
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-sigCh
		quitGracefully(manager)
	}()

	systray.Run(func() { onReady(manager, panelURL) }, func() {
		// 兜底：systray 退出回调（幂等）
		manager.Stop()
	})
}

// quitGracefully 先断开 VPN（可能耗时数秒），再结束托盘循环
func quitGracefully(manager *Manager) {
	go func() {
		manager.Stop()
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

func onReady(m *Manager, panelURL string) {
	systray.SetIcon(iconBytes)
	systray.SetTitle("XTU")
	systray.SetTooltip("XTU-Connect 湘潭大学校园网")
	systray.SetTemplateIcon(iconBytes, iconBytes)

	mStatus := systray.AddMenuItem("状态：加载中", "当前连接状态")
	mStatus.Disable()
	mToggle := systray.AddMenuItem("连接", "连接校园网 VPN")
	mRestart := systray.AddMenuItem("重新连接", "断开并重新连接")
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
			case <-mPanel.ClickedCh:
				if panelURL != "" {
					openURL(panelURL)
				}
			case <-mLog.ClickedCh:
				openFile(m.LogPath())
			case <-mQuit.ClickedCh:
				quitGracefully(m)
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
