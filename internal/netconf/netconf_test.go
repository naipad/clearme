package netconf

import (
	"net"
	"slices"
	"strings"
	"testing"
)

func TestRenderInterfacesDHCPAndSLAAC(t *testing.T) {
	n := Net{
		Interface: "eth0",
		MAC:       "aa:bb:cc:dd:ee:ff",
		HasV4:     true,
		DHCP:      true,
		IP:        "192.0.2.10",
		Prefix:    24,
		Gateway:   "192.0.2.1",
		V6:        IPv6{Interface: "eth0", Address: "2001:db8::10", Prefix: 64, SLAAC: true},
	}
	out := RenderInterfaces(n, RenderOptions{Release: 13, DNS4: []string{"1.1.1.1"}})
	for _, want := range []string{
		"source /etc/network/interfaces.d/*",
		"auto lo\niface lo inet loopback",
		"# mac aa:bb:cc:dd:ee:ff",
		"auto eth0",
		"iface eth0 inet dhcp",
		"iface eth0 inet6 auto",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("interfaces missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "dns-nameservers") {
		t.Error("DHCP 模式不应写死 DNS")
	}
	if strings.Contains(out, "inet6 static") {
		t.Error("SLAAC 不应写成静态")
	}
}

func TestRenderInterfacesStaticV6(t *testing.T) {
	n := Net{
		Interface: "ens3",
		HasV4:     true,
		Static:    true,
		IP:        "192.0.2.10",
		Prefix:    24,
		Gateway:   "192.0.2.1",
		V6: IPv6{
			Interface: "ens3", Address: "2001:db8::10", Prefix: 64,
			Gateway: "fe80::1", Static: true, DisableRA: true, DisableAutoconf: true,
			Extra: []string{"2001:db8::11/64", "2001:db8::12/64"},
		},
	}
	out := RenderInterfaces(n, RenderOptions{
		Release: 13, DNS4: []string{"1.1.1.1"}, DNS6: []string{"2606:4700:4700::1111"},
		WriteDNS4: true, WriteDNS6: true,
	})
	for _, want := range []string{
		"iface ens3 inet static",
		"    address 192.0.2.10/24",
		"    gateway 192.0.2.1",
		"    dns-nameservers 1.1.1.1",
		"iface ens3 inet6 static",
		"    address 2001:db8::10/64",
		"    gateway fe80::1",
		"    post-up ip -6 addr add 2001:db8::11/64 dev ens3",
		"    post-up ip -6 addr add 2001:db8::12/64 dev ens3",
		"    dns-nameserver 2606:4700:4700::1111",
		"    accept_ra 0",
		"    autoconf 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("interfaces missing %q\n%s", want, out)
		}
	}
}

func TestRenderInterfacesOnlinkGateway(t *testing.T) {
	n := Net{
		Interface: "eth0",
		V6: IPv6{
			Interface: "eth0", Address: "2001:db8::10", Prefix: 64,
			Gateway: "2001:db9::1", Static: true,
		},
	}
	out := RenderInterfaces(n, RenderOptions{Release: 12})
	if strings.Contains(out, "    gateway 2001:db9::1") {
		t.Error("子网外网关不应直接写成 gateway 选项")
	}
	for _, want := range []string{
		"post-up ip -6 route add 2001:db9::1 dev eth0",
		"post-up ip -6 route add default via 2001:db9::1 dev eth0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("interfaces missing %q\n%s", want, out)
		}
	}
}

func TestRenderInterfacesDHCPv6Release(t *testing.T) {
	n := Net{
		Interface: "eth0", HasV4: true, DHCP: true, IP: "192.0.2.10", Prefix: 24,
		V6: IPv6{Interface: "eth0", Address: "2001:db8::10", Prefix: 128, DHCPv6: true},
	}
	if out := RenderInterfaces(n, RenderOptions{Release: 12}); !strings.Contains(out, "iface eth0 inet6 dhcp") {
		t.Errorf("Debian 12 应使用 inet6 dhcp\n%s", out)
	}
	out := RenderInterfaces(n, RenderOptions{Release: 13})
	if !strings.Contains(out, "iface eth0 inet6 auto") {
		t.Errorf("Debian 13 应使用 inet6 auto\n%s", out)
	}
	if strings.Contains(out, "inet6 dhcp") {
		t.Error("Debian 13 不应使用 inet6 dhcp")
	}
}

func TestRenderInterfacesV6OnOtherInterface(t *testing.T) {
	n := Net{
		Interface: "eth0", HasV4: true, DHCP: true, IP: "192.0.2.10", Prefix: 24,
		V6: IPv6{Interface: "eth1", Address: "2001:db8::10", Prefix: 64, SLAAC: true},
	}
	out := RenderInterfaces(n, RenderOptions{Release: 13})
	if !strings.Contains(out, "auto eth1") || !strings.Contains(out, "iface eth1 inet6 auto") {
		t.Errorf("应单独配置 IPv6 所在网卡\n%s", out)
	}
}

func TestRenderResolvConf(t *testing.T) {
	out := RenderResolvConf([]string{"1.1.1.1", "2606:4700:4700::1111"},
		[]string{"search example.com", "", "# ignored", "options edns0"})
	want := "nameserver 1.1.1.1\nnameserver 2606:4700:4700::1111\nsearch example.com\noptions edns0\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("unexpected resolv.conf:\n%s", out)
	}
}

func TestFallbackDNS(t *testing.T) {
	v4, v6 := FallbackDNS(false)
	if v4[0] != "1.1.1.1" || v6[0] != "2606:4700:4700::1111" {
		t.Errorf("unexpected global DNS: %v %v", v4, v6)
	}
	v4, v6 = FallbackDNS(true)
	if v4[0] != "223.5.5.5" || v6[0] != "2400:3200::1" {
		t.Errorf("unexpected china DNS: %v %v", v4, v6)
	}
}

func TestV6GatewayOffSubnet(t *testing.T) {
	cases := []struct {
		gw, addr string
		prefix   int
		want     bool
	}{
		{"fe80::1", "2001:db8::10", 64, false},
		{"2001:db8::1", "2001:db8::10", 64, false},
		{"2001:db9::1", "2001:db8::10", 64, true},
		{"2001:db8:0:1::1", "2001:db8::10", 48, false},
	}
	for _, c := range cases {
		if got := v6GatewayOffSubnet(c.gw, c.addr, c.prefix); got != c.want {
			t.Errorf("v6GatewayOffSubnet(%q, %q, %d) = %v, want %v", c.gw, c.addr, c.prefix, got, c.want)
		}
	}
}

func TestMaskHelpers(t *testing.T) {
	if got := MaskToPrefix("255.255.255.0"); got != 24 {
		t.Errorf("MaskToPrefix = %d, want 24", got)
	}
	if got := PrefixToMask(20); got != "255.255.240.0" {
		t.Errorf("PrefixToMask = %q", got)
	}
	if p, changed := WidenForGateway("192.168.1.10", "192.168.0.1", 24); !changed || p != 23 {
		t.Errorf("WidenForGateway = %d, %v; want 23, true", p, changed)
	}
	if p, changed := WidenForGateway("192.168.1.10", "192.168.1.1", 24); changed || p != 24 {
		t.Errorf("WidenForGateway = %d, %v; want 24, false", p, changed)
	}
}

// TestParseRA 检查 rdisc6 输出里的 A 位和 M 位解析。
func TestParseRA(t *testing.T) {
	// 摘自 rdisc6 的真实输出片段。
	ra := `Hop limit                 :           64 (      0x40)
Stateful address conf.    :           No
Stateful other conf.      :          Yes
 Prefix                   : 2001:db8::/64
  Autonomous address conf.:          Yes
`
	tests := []struct {
		name string
		out  string
		want raInfo
	}{
		{"A 位和 M 位都置位", "Stateful address configuration: yes\nAutonomous address configuration: yes\n",
			raInfo{SLAAC: true, DHCPv6: true}},
		{"只有 M 位", "Stateful address configuration: yes\nAutonomous address configuration: no\n",
			raInfo{DHCPv6: true}},
		{"只有 A 位", "Stateful address configuration: no\nAutonomous address configuration: yes\n",
			raInfo{SLAAC: true}},
		{"真实输出", ra, raInfo{SLAAC: true}},
		{"空输出", "", raInfo{}},
	}
	for _, c := range tests {
		if got := parseRA(c.out); got != c.want {
			t.Errorf("%s: parseRA() = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestChooseV6Mode 检查 SLAAC/DHCPv6 的优先级和信号探测的惰性。
func TestChooseV6Mode(t *testing.T) {
	tests := []struct {
		name string
		ra   raInfo
		// slaac/dhcp 是启发式信号的返回值，调用次数用于检查惰性。
		slaac, dhcp bool
		want        v6Mode
	}{
		{"A 位与 M 位同时置位时选 SLAAC", raInfo{SLAAC: true, DHCPv6: true}, true, true, v6SLAACMode},
		{"只有 M 位时选 DHCPv6", raInfo{DHCPv6: true}, true, false, v6DHCPMode},
		{"没有 RA 标志时 SLAAC 信号优先", raInfo{}, true, true, v6SLAACMode},
		{"只有 DHCPv6 信号", raInfo{}, false, true, v6DHCPMode},
		{"没有任何信号时默认 SLAAC", raInfo{}, false, false, v6SLAACMode},
	}
	for _, c := range tests {
		var slaacCalls, dhcpCalls int
		slaacSignal := func() bool { slaacCalls++; return c.slaac }
		dhcpSignal := func() bool { dhcpCalls++; return c.dhcp }
		if got := chooseV6Mode(c.ra, slaacSignal, dhcpSignal); got != c.want {
			t.Errorf("%s: chooseV6Mode() = %v, want %v", c.name, got, c.want)
		}
		if c.ra.SLAAC || c.ra.DHCPv6 {
			if slaacCalls+dhcpCalls != 0 {
				t.Errorf("%s: RA 标志已决定结果，不应再执行信号探测", c.name)
			}
		}
	}
}

func TestValidIface(t *testing.T) {
	for _, name := range []string{"eth0", "ens3", "enp0s31f6", "br-1a2b3c", "eth0.100", "eth0:1"} {
		if !validIface(name) {
			t.Errorf("%q should be valid", name)
		}
	}
	for _, name := range []string{"", "eth0; rm -rf /", "eth 0", "a/b", "$(id)"} {
		if validIface(name) {
			t.Errorf("%q should be rejected", name)
		}
	}
}

func TestParseResolvDropsLocalOnlyDNS(t *testing.T) {
	data := "# comment\n" +
		"nameserver 127.0.0.53\n" +
		"nameserver 100.100.100.100\n" +
		"nameserver fd7a:115c:a1e0::53\n" +
		"nameserver fe80::1%eth0\n" +
		"nameserver 192.168.1.1\n" +
		"nameserver 2606:4700:4700::1111\n" +
		"search example.com\n"
	reach := func(string) bool { return true }
	dns4, dns6, other := parseResolv(data, reach)
	if want := []string{"192.168.1.1"}; !slices.Equal(dns4, want) {
		t.Errorf("dns4 = %v, want %v", dns4, want)
	}
	if want := []string{"2606:4700:4700::1111"}; !slices.Equal(dns6, want) {
		t.Errorf("dns6 = %v, want %v", dns6, want)
	}
	if want := []string{"search example.com"}; !slices.Equal(other, want) {
		t.Errorf("other = %v, want %v", other, want)
	}
}

func TestParseResolvDropsTunnelRoute(t *testing.T) {
	data := "nameserver 10.8.0.1\nnameserver 1.1.1.1\n"
	reach := func(host string) bool { return host != "10.8.0.1" }
	dns4, _, _ := parseResolv(data, reach)
	if want := []string{"1.1.1.1"}; !slices.Equal(dns4, want) {
		t.Errorf("dns4 = %v, want %v", dns4, want)
	}
}

func TestUsableDNS(t *testing.T) {
	for _, s := range []string{"127.0.0.53", "100.100.100.100", "100.127.255.254", "fd7a:115c:a1e0::53", "fe80::1"} {
		if usableDNS(net.ParseIP(s)) {
			t.Errorf("%s should be rejected", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "192.168.1.1", "10.0.0.2", "2606:4700:4700::1111"} {
		if !usableDNS(net.ParseIP(s)) {
			t.Errorf("%s should be accepted", s)
		}
	}
}
