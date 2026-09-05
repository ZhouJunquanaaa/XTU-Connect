package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"xtu-connect/configs"
)

//go:embed panel.html
var panelHTML []byte

// startPanelServer 在 127.0.0.1 上启动控制面板，返回实际访问地址。
// proxy 提供 /proxy.pac 的动态 PAC 脚本（按系统里检测到的链式代理生成）
func startPanelServer(m *Manager, proxy *sysProxy) (string, error) {
	var listener net.Listener
	var addr string
	for port := 58081; port <= 58090; port++ {
		addr = fmt.Sprintf("127.0.0.1:%d", port)
		l, err := net.Listen("tcp", addr)
		if err == nil {
			listener = l
			break
		}
	}
	if listener == nil {
		return "", fmt.Errorf("控制面板端口 58081-58090 均被占用")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(panelHTML)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, m.Status())
	})
	mux.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Start(); err != nil {
			writeJSON(w, map[string]string{"ok": "false", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) {
		m.Stop()
		writeJSON(w, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/restart", func(w http.ResponseWriter, r *http.Request) {
		m.Restart()
		writeJSON(w, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/credentials", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
			writeJSON(w, map[string]string{"ok": "false", "error": "学号和密码不能为空"})
			return
		}
		// 收进账号簿（同用户名则更新密码），再写为当前账号
		if m.AccountBook != nil {
			if err := m.AccountBook.Add(req.Username, req.Password); err != nil {
				writeJSON(w, map[string]string{"ok": "false", "error": "保存到账号簿失败: " + err.Error()})
				return
			}
		}
		cfg := configs.Default()
		cfg.Username = req.Username
		cfg.Password = req.Password
		if err := configs.SaveCredentials(cfg); err != nil {
			writeJSON(w, map[string]string{"ok": "false", "error": err.Error()})
			return
		}
		if m.Running() {
			go m.Restart()
		}
		writeJSON(w, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/accounts/switch", func(w http.ResponseWriter, r *http.Request) {
		if m.AccountBook == nil {
			writeJSON(w, map[string]string{"ok": "false", "error": "账号簿不可用"})
			return
		}
		var req struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
			writeJSON(w, map[string]string{"ok": "false", "error": "用户名不能为空"})
			return
		}
		password, err := m.AccountBook.Password(req.Username)
		if err != nil {
			writeJSON(w, map[string]string{"ok": "false", "error": err.Error()})
			return
		}
		cfg := configs.Default()
		cfg.Username = req.Username
		cfg.Password = password
		if err := configs.SaveCredentials(cfg); err != nil {
			writeJSON(w, map[string]string{"ok": "false", "error": err.Error()})
			return
		}
		// 已连接则用新账号重连（旧会话由服务端单会话策略踢下线），未连接则直接连接
		go func() {
			if m.Running() {
				m.Restart()
			} else {
				_ = m.Start()
			}
		}()
		writeJSON(w, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/accounts/delete", func(w http.ResponseWriter, r *http.Request) {
		if m.AccountBook == nil {
			writeJSON(w, map[string]string{"ok": "false", "error": "账号簿不可用"})
			return
		}
		var req struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
			writeJSON(w, map[string]string{"ok": "false", "error": "用户名不能为空"})
			return
		}
		if req.Username == configs.SavedUsername() {
			writeJSON(w, map[string]string{"ok": "false", "error": "不能删除当前登录的账号，请先切换到其他账号"})
			return
		}
		if err := m.AccountBook.Remove(req.Username); err != nil {
			writeJSON(w, map[string]string{"ok": "false", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/proxy.pac", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Write([]byte(proxy.PacScript()))
	})
	mux.HandleFunc("/api/log", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"lines": m.LogTail(200)})
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	return "http://" + addr, nil
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
