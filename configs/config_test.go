package configs

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestSavedUsername(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".config", "xtu-connect", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if got := SavedUsername(); got != "" {
		t.Fatalf("无配置文件时应返回空串, got %q", got)
	}
	content := "protocol = \"easyconnect\"\nusername = \"202521633358\"\npassword = \"secret\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := SavedUsername(); got != "202521633358" {
		t.Fatalf("SavedUsername = %q, want 202521633358", got)
	}
}
