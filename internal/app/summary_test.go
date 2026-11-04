package app

import (
	"strings"
	"testing"

	"github.com/naipad/clearme/internal/netconf"
)

// TestLoginAddressText 检查登录地址优先使用 IPv4，IPv6 带方括号和端口。
func TestLoginAddressText(t *testing.T) {
	tests := []struct {
		name string
		v4   string
		v6   string
		port int
		want string
	}{
		{"only-v4", "192.0.2.10", "", 22, "192.0.2.10:22"},
		{"only-v6", "", "2001:db8::10", 2222, "[2001:db8::10]:2222"},
		{"both", "192.0.2.10", "2001:db8::10", 22, "192.0.2.10:22 · [2001:db8::10]:22"},
		{"none", "", "", 22, "未检测到可用地址"},
	}
	for _, tt := range tests {
		if got := loginAddressText(tt.v4, tt.v6, tt.port); got != tt.want {
			t.Errorf("%s: loginAddressText = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestSummarySections 检查配置摘要和登录信息分成两个区块，重要信息不重复。
func TestSummarySections(t *testing.T) {
	o := &Options{Username: "root", SSHPort: 22, Password: "secret"}
	in := summaryInput{
		o: o, suite: "trixie", arch: "amd64", disk: "/dev/sda",
		n:        netconf.Net{Interface: "eth0", HasV4: true, IP: "192.0.2.10"},
		hostname: "srv", mirrorURL: "https://deb.debian.org/debian",
		password: "secret",
		loginV4:  "192.0.2.10",
	}

	keys := map[string]bool{}
	for _, r := range in.rows() {
		keys[r.key] = true
	}
	for _, gone := range []string{"基本配置", "账户", "主机名", "登录密码", "软件包", "其他"} {
		if keys[gone] {
			t.Errorf("配置摘要不应再包含 %q", gone)
		}
	}
	if !keys["目标"] || !keys["自动更新"] || !keys["网卡"] {
		t.Errorf("配置摘要缺少目标、自动更新或网卡行：%v", keys)
	}

	login := in.loginRows()
	want := []string{"主机名称", "登录地址", "登录用户", "登录密码"}
	if len(login) != len(want) {
		t.Fatalf("登录信息行数 = %d, want %d", len(login), len(want))
	}
	for i, key := range want {
		if login[i].key != key {
			t.Errorf("登录信息第 %d 行 = %q, want %q", i, login[i].key, key)
		}
	}
	if got := login[1].value; got != "192.0.2.10:22" {
		t.Errorf("登录地址 = %q", got)
	}
	if got := login[3].value; got != "secret" {
		t.Errorf("登录密码应直接显示密码，不再附加说明：%q", got)
	}
}

// TestIPv4Text 检查 DHCP 也会显示租到的地址和网关。
func TestIPv4Text(t *testing.T) {
	tests := []struct {
		name string
		n    netconf.Net
		want string
	}{
		{"static", netconf.Net{HasV4: true, Static: true,
			IP: "45.129.8.7", Prefix: 24, Gateway: "45.129.8.1"},
			"静态 45.129.8.7/24 → 45.129.8.1"},
		{"dhcp", netconf.Net{HasV4: true, DHCP: true,
			IP: "45.129.8.7", Prefix: 24, Gateway: "45.129.8.1"},
			"DHCP 45.129.8.7/24 → 45.129.8.1"},
		{"dhcp-no-lease", netconf.Net{HasV4: true, DHCP: true}, "DHCP"},
		{"none", netconf.Net{}, "无"},
	}
	for _, tt := range tests {
		if got := ipv4Text(tt.n); got != tt.want {
			t.Errorf("%s: ipv4Text = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestIPv6Text 检查 IPv6 使用与 IPv4 相同的展示格式。
func TestIPv6Text(t *testing.T) {
	tests := []struct {
		name string
		v6   netconf.IPv6
		want string
	}{
		{"static", netconf.IPv6{Interface: "ens5", Address: "2001:db8::10", Prefix: 64,
			Gateway: "2001:db8::1", Static: true},
			"静态 2001:db8::10/64 → 2001:db8::1"},
		{"dhcpv6", netconf.IPv6{Interface: "ens5", Address: "2001:db8::10", Prefix: 64,
			DHCPv6: true},
			"DHCPv6 2001:db8::10/64"},
		{"slaac", netconf.IPv6{Interface: "ens5", Address: "2001:db8::10", Prefix: 64,
			SLAAC: true, Extra: []string{"2001:db8::11"}},
			"SLAAC 2001:db8::10/64 · 另 1 个地址"},
	}
	for _, tt := range tests {
		if got := ipv6Text(netconf.Net{V6: tt.v6}); got != tt.want {
			t.Errorf("%s: ipv6Text = %q, want %q", tt.name, got, tt.want)
		}
	}
	if got := ipv6Text(netconf.Net{}); got != "无" {
		t.Errorf("ipv6Text(空) = %q", got)
	}
}

// TestNicText 检查网卡行显示 MAC；IPv6 在另一块网卡时也列出。
func TestNicText(t *testing.T) {
	n := netconf.Net{
		Interface: "eth0", MAC: "00:16:3e:4f:76:5b",
		V6: netconf.IPv6{Interface: "eth0", MAC: "00:16:3e:4f:76:5b"},
	}
	if got, want := nicText(n), "eth0 (00:16:3e:4f:76:5b)"; got != want {
		t.Errorf("nicText = %q, want %q", got, want)
	}

	n.V6.Interface = "eth1"
	n.V6.MAC = "aa:bb:cc:dd:ee:ff"
	want := "eth0 (00:16:3e:4f:76:5b) · eth1 (aa:bb:cc:dd:ee:ff)"
	if got := nicText(n); got != want {
		t.Errorf("nicText = %q, want %q", got, want)
	}
}

// TestSummaryNetworkRows 检查网络区块按“网卡 / IPv4 / IPv6 / DNS”展示。
func TestSummaryNetworkRows(t *testing.T) {
	n := netconf.Net{
		Interface: "eth0", MAC: "00:16:3e:4f:76:5b",
		HasV4: true, DHCP: true,
		IP: "198.23.215.93", Prefix: 26, Gateway: "198.23.215.65",
		DNS4: []string{"100.100.2.136", "100.100.2.138"},
		V6: netconf.IPv6{
			Interface: "eth0", Address: "2607:9d00:2000:c9::627e:dba4", Prefix: 64,
			Gateway: "2607:9d00:2000:c9::1", SLAAC: true,
		},
	}
	o := &Options{Timezone: "Asia/Shanghai", NTP: "ntp.aliyun.com"}
	in := summaryInput{o: o, n: n}
	rows := map[string]string{}
	for _, r := range in.rows() {
		rows[r.key] = r.value
	}
	want := map[string]string{
		"网卡":   "eth0 (00:16:3e:4f:76:5b)",
		"IPv4": "DHCP 198.23.215.93/26 → 198.23.215.65",
		"IPv6": "SLAAC 2607:9d00:2000:c9::627e:dba4/64 → 2607:9d00:2000:c9::1",
		"DNS":  "100.100.2.136 100.100.2.138",
	}
	for key, value := range want {
		if rows[key] != value {
			t.Errorf("%s = %q, want %q", key, rows[key], value)
		}
	}
}

// TestTargetRowAndTitle 检查目标行只保留引导方式和磁盘，系统信息移到标题。
func TestTargetRowAndTitle(t *testing.T) {
	o := &Options{Filesystem: "ext4", Firmware: true}
	in := summaryInput{o: o, suite: "trixie", arch: "amd64", disk: "/dev/vda", efi: true}
	if got, want := targetText(o, in), "UEFI (ESP 106 MB) · /dev/vda (ext4)"; got != want {
		t.Errorf("targetText = %q, want %q", got, want)
	}
	if got, want := summaryTitle(o, in), "Debian 13 (trixie, amd64, firmware)"; got != want {
		t.Errorf("summaryTitle = %q, want %q", got, want)
	}

	o.Firmware = false
	if got, want := summaryTitle(o, in), "Debian 13 (trixie, amd64)"; got != want {
		t.Errorf("summaryTitle(no firmware) = %q, want %q", got, want)
	}
	in.efi = false
	if got, want := targetText(o, in), "BIOS · /dev/vda (ext4)"; got != want {
		t.Errorf("targetText(BIOS) = %q, want %q", got, want)
	}
	if strings.ContainsAny(targetText(o, in), "（）") {
		t.Error("目标行不应使用全角括号")
	}
}

// TestKeepTextMounts 检查挂载点按分区号匹配，不会因为不可挂载分区而错位。
func TestKeepTextMounts(t *testing.T) {
	got := keepText("/dev/sda", []int{2, 3, 4}, map[int]string{2: "/data", 4: "/data2"}, false)
	want := "sda2 → /data，sda3，sda4 → /data2"
	if got != want {
		t.Errorf("keepText = %q, want %q", got, want)
	}
}
