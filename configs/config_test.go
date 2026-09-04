package configs

import "testing"

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Protocol != "easyconnect" {
		t.Fatalf("Protocol = %q, want easyconnect", cfg.Protocol)
	}
	if cfg.ServerAddress != "vpn.xtu.edu.cn" {
		t.Fatalf("ServerAddress = %q, want vpn.xtu.edu.cn", cfg.ServerAddress)
	}
	if cfg.ServerPort != 443 {
		t.Fatalf("ServerPort = %d, want 443", cfg.ServerPort)
	}
	if cfg.SocksBind != "127.0.0.1:1080" || cfg.HTTPBind != "127.0.0.1:1081" {
		t.Fatalf("proxy defaults = %q, %q", cfg.SocksBind, cfg.HTTPBind)
	}
}
