//go:build !windows

package main

import "syscall"

// rootChildAlive 通过信号探测判断 root 子进程是否存活
//（对 root 进程 kill -0 返回 EPERM，同样代表存活）
func rootChildAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
