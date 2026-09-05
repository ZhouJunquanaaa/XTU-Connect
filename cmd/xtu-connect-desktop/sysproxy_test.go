package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseWindowsProxyServer(t *testing.T) {
	cases := []struct {
		in   string
		kind string
		addr string
	}{
		{"", "", ""},
		{"127.0.0.1:7890", "http", "127.0.0.1:7890"},
		{` "127.0.0.1:7890" `, "http", "127.0.0.1:7890"},
		{"http=127.0.0.1:7890;https=127.0.0.1:7890;ftp=127.0.0.1:7890", "http", "127.0.0.1:7890"},
		{"https=10.0.0.1:8443;socks=127.0.0.1:7891", "http", "10.0.0.1:8443"},
		{"socks=127.0.0.1:7891", "socks", "127.0.0.1:7891"},
		{"http=;https=;socks=", "", ""},
	}
	for _, c := range cases {
		got := parseWindowsProxyServer(c.in)
		if got.kind != c.kind || got.addr != c.addr {
			t.Errorf("parseWindowsProxyServer(%q) = %+v, want kind=%q addr=%q", c.in, got, c.kind, c.addr)
		}
	}
}

func TestSetScriptLocked(t *testing.T) {
	sp := &sysProxy{}

	// 无链式代理：其余流量 DIRECT
	sp.setScriptLocked(chainProxy{})
	script := sp.PacScript()
	for _, want := range []string{
		`return "PROXY ` + localHTTPProxy + `; SOCKS5 ` + localSocksProxy + `"`,
		`*.xtu.edu.cn`,
		`isInNet(host, "10.0.0.0", "255.0.0.0")`,
		`isInNet(host, "172.16.0.0", "255.240.0.0")`,
		`return "DIRECT"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("PAC 缺少 %q\n脚本:\n%s", want, script)
		}
	}

	// 链式接管 Clash 系统代理：其余流量走原代理再回落 DIRECT
	sp.setScriptLocked(chainProxy{kind: "http", addr: "127.0.0.1:7890"})
	script = sp.PacScript()
	if !strings.Contains(script, `return "PROXY 127.0.0.1:7890; DIRECT"`) {
		t.Errorf("链式 PAC 缺少回落规则\n脚本:\n%s", script)
	}
	if sp.ChainText() != "127.0.0.1:7890" {
		t.Errorf("ChainText = %q, want 127.0.0.1:7890", sp.ChainText())
	}

	// SOCKS 型手动代理
	sp.setScriptLocked(chainProxy{kind: "socks", addr: "127.0.0.1:7891"})
	if !strings.Contains(sp.PacScript(), `return "SOCKS5 127.0.0.1:7891; DIRECT"`) {
		t.Errorf("SOCKS 链式 PAC 不正确\n脚本:\n%s", sp.PacScript())
	}
}

func TestDarwinProxyField(t *testing.T) {
	out := "Enabled: Yes\nServer: 127.0.0.1\nPort: 7890\nAuthenticated Proxy Enabled: 0\n"
	if got := darwinProxyField(out, "Server"); got != "127.0.0.1" {
		t.Errorf("Server = %q", got)
	}
	if got := darwinProxyField(out, "Port"); got != "7890" {
		t.Errorf("Port = %q", got)
	}
	// 不能误匹配 "Authenticated Proxy Enabled"
	if got := darwinProxyField(out, "Enabled"); got != "Yes" {
		t.Errorf("Enabled = %q", got)
	}
}

func TestPanelServesDynamicPac(t *testing.T) {
	m := NewManager()
	sp := newSysProxy()
	url, err := startPanelServer(m, sp)
	if err != nil {
		t.Fatalf("startPanelServer: %v", err)
	}
	sp.SetPacURL(url + "/proxy.pac")

	sp.setScriptLocked(chainProxy{kind: "http", addr: "127.0.0.1:7890"})

	resp, err := http.Get(url + "/proxy.pac")
	if err != nil {
		t.Fatalf("GET /proxy.pac: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "PROXY 127.0.0.1:7890") {
		t.Errorf("/proxy.pac 未反映链式代理:\n%s", body)
	}
}
