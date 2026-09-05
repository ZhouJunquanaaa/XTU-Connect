package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSSHConfigApplyOnMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	s := &sshConfig{path: path}

	s.Apply()
	content := readFile(t, path)
	if !strings.Contains(content, sshManagedBegin) ||
		!strings.Contains(content, `ProxyCommand nc -X 5 -x 127.0.0.1:1080 %h %p`) {
		t.Fatalf("托管块未写入:\n%s", content)
	}
	if s.State() != "on" {
		t.Fatalf("State = %q, want on", s.State())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows 不支持 POSIX 权限位（新建文件恒为 0666），只校验 Unix 行为
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("新文件权限 = %v, want 600", info.Mode().Perm())
	}
}

func TestSSHConfigApplyIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	s := &sshConfig{path: path}

	s.Apply()
	s.applied = false // 模拟重复触发
	s.Apply()
	s.applied = false
	s.Apply()
	if got := strings.Count(readFile(t, path), sshManagedBegin); got != 1 {
		t.Fatalf("托管块出现 %d 次, want 1", got)
	}
}

func TestSSHConfigApplyPreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	userContent := "Host foo\n  HostName 1.2.3.4\n  User bob\n"
	writeFile(t, path, userContent)
	s := &sshConfig{path: path}

	s.Apply()
	content := readFile(t, path)
	if !strings.HasPrefix(content, userContent) {
		t.Fatalf("用户内容被破坏:\n%s", content)
	}

	s.Remove()
	if got := readFile(t, path); got != userContent {
		t.Fatalf("Remove 后未恢复原样:\n%q", got)
	}
	if s.State() != "off" {
		t.Fatalf("State = %q, want off", s.State())
	}
}

func TestSSHConfigRemoveOnFileWithoutBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	userContent := "Host foo\n  User bob\n"
	writeFile(t, path, userContent)
	s := &sshConfig{path: path}

	s.Remove()
	if got := readFile(t, path); got != userContent {
		t.Fatalf("无托管块时 Remove 改动了文件:\n%q", got)
	}
}

func TestSSHConfigRemoveTruncatedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, "Host foo\n  User bob\n\n"+sshManagedBegin+"\nMatch host 截断残留")
	s := &sshConfig{path: path}

	s.Remove()
	if got := readFile(t, path); strings.Contains(got, "截断残留") || strings.Contains(got, sshManagedBegin) {
		t.Fatalf("截断块未清理干净:\n%q", got)
	}
}

func TestSSHConfigApplyRewritesStaleBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	stale := sshManagedBegin + "\n# 旧版本内容\nMatch host \"10.*\"\n    ProxyCommand nc -x 1080 %h %p\n" + sshManagedEnd + "\n"
	writeFile(t, path, stale)
	s := &sshConfig{path: path}

	s.Apply()
	content := readFile(t, path)
	if !strings.Contains(content, sshManagedBody) {
		t.Fatalf("旧内容未被更新:\n%s", content)
	}
	if strings.Contains(content, "旧版本内容") {
		t.Fatalf("旧内容残留:\n%s", content)
	}
}

func TestSSHConfigMissingEndMarkerRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	userContent := "Host foo\n  User bob\n"
	writeFile(t, path, userContent+"\n"+sshManagedBegin+"\nMatch host \"10.*\"\n    ProxyCommand broken %h %p\n")
	s := &sshConfig{path: path}

	s.Apply()
	content := readFile(t, path)
	if strings.Contains(content, "ProxyCommand broken") {
		t.Fatalf("无结束标记的残留未重写:\n%s", content)
	}
	if !strings.Contains(content, sshManagedBody) || !strings.Contains(content, sshManagedEnd) {
		t.Fatalf("托管块不完整:\n%s", content)
	}
	if !strings.HasPrefix(content, userContent) {
		t.Fatalf("用户内容被破坏:\n%s", content)
	}
}

func TestSSHConfigUnsupportedPlatform(t *testing.T) {
	s := &sshConfig{path: ""} // 模拟不支持的目录解析
	s.Apply()
	s.Remove()
	if s.State() != "unsupported" {
		t.Fatalf("State = %q, want unsupported", s.State())
	}
}
