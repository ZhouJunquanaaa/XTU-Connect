package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// sysDNS 在整机分流（TUN）模式下，把系统 DNS 指向本程序（127.0.0.1），
// 由程序内置解析器分流：内网域名走校内 DNS，其余走上游。
// 断开或退出时恢复各网络服务原来的 DNS 设置。
type sysDNS struct {
	mu      sync.Mutex
	applied bool
	services []dnsBackup
}

type dnsBackup struct {
	name    string
	servers string // 原始值；空串表示「自动(DHCP)」
}

func newSysDNS() *sysDNS { return &sysDNS{} }

// Apply 把所有已启用网络服务的 DNS 设为 127.0.0.1（保存原值）
func (d *sysDNS) Apply() {
	d.mu.Lock()
	if d.applied {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	if runtime.GOOS != "darwin" {
		// Windows/Linux 的 DNS 由 root 辅助脚本/TUN 自行处理或不支持
		return
	}
	var backups []dnsBackup
	for _, svc := range darwinEnabledServices() {
		out, err := exec.Command("networksetup", "-getdnsservers", svc).Output()
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "aren't any DNS Servers") {
			text = ""
		}
		if err := exec.Command("networksetup", "-setdnsservers", svc, "127.0.0.1").Run(); err != nil {
			continue
		}
		backups = append(backups, dnsBackup{name: svc, servers: text})
	}
	if len(backups) == 0 {
		return
	}
	d.mu.Lock()
	d.applied = true
	d.services = backups
	d.mu.Unlock()
}

// Restore 恢复 DNS 原值（幂等）
func (d *sysDNS) Restore() {
	d.mu.Lock()
	backups := d.services
	d.services = nil
	d.applied = false
	d.mu.Unlock()

	if runtime.GOOS != "darwin" {
		return
	}
	for _, b := range backups {
		value := b.servers
		if value == "" {
			value = "Empty" // 恢复为 DHCP 自动
		}
		_ = exec.Command("networksetup", "-setdnsservers", b.name, value).Run()
	}
}

func (d *sysDNS) Active() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.applied
}

var _ = os.Environ // 保持导入（windows/linux 分支预留）
