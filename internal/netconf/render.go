package netconf

import (
	"fmt"
	"net"
	"strings"
)

// RenderOptions 控制目标系统 /etc/network/interfaces 的生成方式。
type RenderOptions struct {
	// Release 是目标 Debian 主版本号。13 起 ifupdown 使用 dhcpcd-base，
	// DHCPv6 需要写成 inet6 auto。
	Release int

	DNS4 []string
	DNS6 []string
	// WriteDNS4/WriteDNS6 表示把 DNS 写进 ifupdown 配置。
	// 静态地址必须写入；DHCP/SLAAC 默认交给协议自身下发。
	WriteDNS4 bool
	WriteDNS6 bool
}

// RenderInterfaces 生成完整的 /etc/network/interfaces：
// 回环、IPv4(DHCP 或静态)、IPv6(SLAAC/DHCPv6/静态、额外地址)以及
// accept_ra/autoconf 开关。返回的内容可以整体覆盖目标系统的同名文件。
func RenderInterfaces(n Net, o RenderOptions) string {
	var b strings.Builder
	b.WriteString("# 由 clearme 生成：沿用重装前的网络配置\n")
	b.WriteString("source /etc/network/interfaces.d/*\n\n")
	b.WriteString("auto lo\niface lo inet loopback\n")

	main := ""
	if validIface(n.Interface) && n.Interface != "lo" {
		main = n.Interface
	}
	v6 := n.V6
	if !v6.Present() {
		v6 = IPv6{}
	}

	if main != "" {
		b.WriteString("\n")
		if n.MAC != "" {
			fmt.Fprintf(&b, "# mac %s\n", n.MAC)
		}
		fmt.Fprintf(&b, "auto %s\n", main)
		if n.HasV4 && (n.IP != "" || n.DHCP) {
			if n.DHCP {
				fmt.Fprintf(&b, "iface %s inet dhcp\n", main)
			} else {
				fmt.Fprintf(&b, "iface %s inet static\n", main)
				fmt.Fprintf(&b, "    address %s\n", v4Address(n))
				if n.Gateway != "" {
					fmt.Fprintf(&b, "    gateway %s\n", n.Gateway)
				}
			}
			if o.WriteDNS4 {
				for _, d := range o.DNS4 {
					fmt.Fprintf(&b, "    dns-nameservers %s\n", d)
				}
			}
		}
		if v6.Present() && v6.Interface == main {
			renderV6(&b, v6, o)
		}
	}

	// IPv6 默认路由可能落在另一块网卡上。
	if v6.Present() && v6.Interface != main && validIface(v6.Interface) {
		b.WriteString("\n# IPv6 位于另一块网卡\n")
		fmt.Fprintf(&b, "auto %s\n", v6.Interface)
		renderV6(&b, v6, o)
	}

	return b.String()
}

// renderV6 生成一个网卡的 IPv6 stanza。
func renderV6(b *strings.Builder, v IPv6, o RenderOptions) {
	switch {
	case v.SLAAC:
		fmt.Fprintf(b, "iface %s inet6 auto\n", v.Interface)
	case v.DHCPv6:
		mode := "dhcp"
		if o.Release >= 13 {
			// Debian 13 的 dhcpcd-base 下 inet+inet6 都写 dhcp 会丢失 IPv4。
			mode = "auto"
		}
		fmt.Fprintf(b, "iface %s inet6 %s\n", v.Interface, mode)
	case v.Static:
		fmt.Fprintf(b, "iface %s inet6 static\n", v.Interface)
		fmt.Fprintf(b, "    address %s/%d\n", v.Address, v.Prefix)
		if v.Gateway != "" {
			if v6GatewayOffSubnet(v.Gateway, v.Address, v.Prefix) {
				// ifupdown 的 inet6 static 不支持 onlink 网关，
				// 先加一条到网关的路由，再加默认路由。
				fmt.Fprintf(b, "    post-up ip -6 route add %s dev %s\n", v.Gateway, v.Interface)
				fmt.Fprintf(b, "    post-up ip -6 route add default via %s dev %s\n", v.Gateway, v.Interface)
			} else {
				fmt.Fprintf(b, "    gateway %s\n", v.Gateway)
			}
		}
		for _, extra := range v.Extra {
			fmt.Fprintf(b, "    post-up ip -6 addr add %s dev %s\n", extra, v.Interface)
		}
	}
	if o.WriteDNS6 {
		for _, d := range o.DNS6 {
			fmt.Fprintf(b, "    dns-nameserver %s\n", d)
		}
	}
	if v.DisableRA {
		b.WriteString("    accept_ra 0\n")
	}
	if v.DisableAutoconf {
		b.WriteString("    autoconf 0\n")
	}
}

// RenderResolvConf 生成目标系统的 /etc/resolv.conf。
func RenderResolvConf(dns, other []string) string {
	var b strings.Builder
	b.WriteString("# 由 clearme 写入\n")
	for _, d := range dns {
		fmt.Fprintf(&b, "nameserver %s\n", d)
	}
	for _, line := range other {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// FallbackDNS 返回备用公共 DNS；china 为真时使用国内服务器。
func FallbackDNS(china bool) (v4, v6 []string) {
	if china {
		return []string{"223.5.5.5", "119.29.29.29"},
			[]string{"2400:3200::1", "2402:4e00::"}
	}
	return []string{"1.1.1.1", "8.8.8.8"},
		[]string{"2606:4700:4700::1111", "2001:4860:4860::8888"}
}

// v4Address 返回 ifupdown 使用的 address 值。
func v4Address(n Net) string {
	p := n.Prefix
	if p <= 0 || p > 32 {
		p = MaskToPrefix(n.Netmask)
	}
	if p <= 0 {
		return n.IP
	}
	return fmt.Sprintf("%s/%d", n.IP, p)
}

// v6GatewayOffSubnet 判断非链路本地网关是否落在地址所在子网之外。
// 这类网关需要先用 post-up 添加 onlink 路由。
func v6GatewayOffSubnet(gateway, address string, prefix int) bool {
	g := net.ParseIP(gateway)
	a := net.ParseIP(address)
	if g == nil || a == nil || g.To4() != nil || a.To4() != nil {
		return false
	}
	if g.IsLinkLocalUnicast() || prefix <= 0 || prefix > 128 {
		return false
	}
	mask := net.CIDRMask(prefix, 128)
	n := net.IPNet{IP: a.Mask(mask), Mask: mask}
	return !n.Contains(g)
}
