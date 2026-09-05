package configs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountBookRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b, err := openAccountBookAt(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Add("", "pw"); err == nil {
		t.Fatal("空用户名应报错")
	}
	if err := b.Add("u1", ""); err == nil {
		t.Fatal("空密码应报错")
	}

	if err := b.Add("u1", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := b.Add("u2", "p2"); err != nil {
		t.Fatal(err)
	}
	if got := b.List(); len(got) != 2 || got[0] != "u1" || got[1] != "u2" {
		t.Fatalf("List = %v", got)
	}
	if !b.Has("u1") || b.Has("u3") {
		t.Fatal("Has 结果不正确")
	}
	if pw, err := b.Password("u1"); err != nil || pw != "p1" {
		t.Fatalf("Password(u1) = %q, %v", pw, err)
	}
	if _, err := b.Password("u3"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("Password(u3) err = %v", err)
	}

	// 覆盖更新
	if err := b.Add("u1", "p1-new"); err != nil {
		t.Fatal(err)
	}
	if got := b.List(); len(got) != 2 {
		t.Fatalf("更新后账号数 = %d, want 2", len(got))
	}
	if pw, _ := b.Password("u1"); pw != "p1-new" {
		t.Fatalf("更新后 Password(u1) = %q", pw)
	}

	// 删除
	if err := b.Remove("u2"); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove("u2"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("重复删除 err = %v", err)
	}

	// 持久化：重新打开后密码仍可解密（同一密钥）
	b2, err := openAccountBookAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pw, err := b2.Password("u1"); err != nil || pw != "p1-new" {
		t.Fatalf("重开后 Password(u1) = %q, %v", pw, err)
	}
}

func TestAccountBookFilesNotPlaintext(t *testing.T) {
	dir := t.TempDir()
	b, err := openAccountBookAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Add("alice", "super-secret"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, accountFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super-secret") {
		t.Fatalf("密码明文落盘了:\n%s", data)
	}
	if _, err := os.ReadFile(filepath.Join(dir, ".secret.key")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, ".secret.key")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("密钥文件权限错误: %v", info)
	}
}

func TestAccountBookKeyReplaced(t *testing.T) {
	dir := t.TempDir()
	b, err := openAccountBookAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Add("u1", "p1"); err != nil {
		t.Fatal(err)
	}

	// 密钥文件丢失 → 生成新密钥 → 旧数据无法解密但报错清晰，新数据可用
	if err := os.Remove(filepath.Join(dir, ".secret.key")); err != nil {
		t.Fatal(err)
	}
	b2, err := openAccountBookAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b2.Password("u1"); err == nil {
		t.Fatal("换密钥后旧密码应解密失败")
	}
	if err := b2.Add("u2", "p2"); err != nil {
		t.Fatal(err)
	}
	if pw, err := b2.Password("u2"); err != nil || pw != "p2" {
		t.Fatalf("换密钥后新账号不可用: %q, %v", pw, err)
	}
}

func TestAccountBookCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := openAccountBookAt(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, accountFileName), []byte("not-json{"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := openAccountBookAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.List(); len(got) != 0 {
		t.Fatalf("损坏文件应视为空账号簿, got %v", got)
	}
	// 损坏后仍可正常添加
	if err := b.Add("u1", "p1"); err != nil {
		t.Fatal(err)
	}
	if pw, _ := b.Password("u1"); pw != "p1" {
		t.Fatalf("Password = %q", pw)
	}
}
