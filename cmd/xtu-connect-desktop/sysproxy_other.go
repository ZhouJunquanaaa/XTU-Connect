//go:build !windows

package main

// Windows 之外的桩实现（darwin 走 networksetup，见 sysproxy.go）
func readWindowsAutoConfig() (url string, existed bool, enabled bool, err error) {
	return "", false, false, nil
}

func readWindowsManualProxy() (enabled bool, server string, err error) {
	return false, "", nil
}

func writeWindowsAutoConfig(string) error { return nil }

func restoreWindowsAutoConfig(string, bool) {}
