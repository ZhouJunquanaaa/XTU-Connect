//go:build !windows

package main

// Windows 之外的桩实现（darwin 走 networksetup，见 sysproxy.go）
func readWindowsAutoConfig() (url string, existed bool, enabled bool, err error) {
	return "", false, false, nil
}

func readWindowsProxyEnable() (bool, error) { return false, nil }

func writeWindowsAutoConfig(string) error { return nil }

func restoreWindowsAutoConfig(string, bool) {}
