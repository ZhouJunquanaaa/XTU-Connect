//go:build windows

package main

import (
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const winInetKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// readWindowsAutoConfig 返回当前 AutoConfigURL 及其是否存在/启用
func readWindowsAutoConfig() (url string, existed bool, enabled bool, err error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, winInetKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false, false, err
	}
	defer k.Close()
	url, _, err = k.GetStringValue("AutoConfigURL")
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			return "", false, false, nil
		}
		return "", false, false, err
	}
	return url, true, url != "", nil
}

// readWindowsManualProxy 返回手动代理的启用状态与 ProxyServer 原始值
// （格式为 "host:port" 或 "http=...;https=...;ftp=...;socks=..."）
func readWindowsManualProxy() (enabled bool, server string, err error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, winInetKey, registry.QUERY_VALUE)
	if err != nil {
		return false, "", err
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil {
		return false, "", err
	}
	server, _, err = k.GetStringValue("ProxyServer")
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			return v != 0, "", nil
		}
		return v != 0, "", err
	}
	return v != 0, server, nil
}

func writeWindowsAutoConfig(url string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, winInetKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("AutoConfigURL", url)
}

func restoreWindowsAutoConfig(backupURL string, hadBackup bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, winInetKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	switch {
	case hadBackup && backupURL != "":
		_ = k.SetStringValue("AutoConfigURL", backupURL)
	default:
		_ = k.DeleteValue("AutoConfigURL")
	}
}
