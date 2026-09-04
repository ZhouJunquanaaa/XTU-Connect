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

// startPanelServer 在 127.0.0.1 上启动控制面板，返回实际访问地址
func startPanelServer(m *Manager) (string, error) {
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
