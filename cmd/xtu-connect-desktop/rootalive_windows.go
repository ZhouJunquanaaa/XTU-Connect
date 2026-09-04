//go:build windows

package main

// rootChildAlive 在 Windows 上的占位实现。
// Windows 暂不支持整机分流（launchRoot 返回不支持），此函数不会被调用。
func rootChildAlive(pid int) bool { return false }
