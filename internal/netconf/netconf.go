// Package netconf 探测当前系统的网络配置，并生成目标系统的 ifupdown 配置。
package netconf

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naipad/clearme/internal/system"
)

// Address 是一个全局地址及其状态标记。
type Address struct {
	IP        string
	Prefix    int
	Dynamic   bool
	Temporary bool
	Mngtmp    bool // mngtmpaddr：SLAAC 的隐私地址模板
	Secondary bool
}

// CIDR 返回 address/prefix 形式。
func (a Address) CIDR() string { return fmt.Sprintf("%s/%d", a.IP, a.Prefix) }

// IPv6 是探测到的 IPv6 配置。
type IPv6 struct {
	Interface string
	MAC       string
	Address   string
	Prefix    int
	Gateway   string
	Extra     []string
	SLAAC     bool
	DHCPv6    bool
	Static    bool

	// 静态配置时关闭 RA/autoconf，避免再次自动生成地址和默认路由。
	DisableRA       bool
	DisableAutoconf bool
}

// Present 表示检测到可用的全局 IPv6 地址。
func (v IPv6) Present() bool { return v.Interface != "" && v.Address != "" }

// Net 是当前系统的网络配置，也是生成目标系统配置的依据。
type Net struct {
	Interface string
	MAC       string

	IP      string
	Prefix  int
	Netmask string
	Gateway string
	Extra4  []string
	DHCP    bool // 当前 IPv4 由 DHCP 获取
	Static  bool // 目标系统写入静态 IPv4
	HasV4   bool

	DNS4        []string
	DNS6        []string
	ResolvOther []string // resolv.conf 里的 search/domain/options

	V6 IPv6
}

// Present 表示检测到 IPv4 或 IPv6 配置。
func (n Net) Present() bool { return n.HasV4 || n.V6.Present() }

// DNS 返回 IPv4/IPv6 DNS 的合并去重结果。
func (n Net) DNS() []string {
	out := make([]string, 0, len(n.DNS4)+len(n.DNS6))
	out = append(out, n.DNS4...)
	out = append(out, n.DNS6...)
	return unique(out)
}

// DetectOn 探测网络配置；iface 为 auto 时根据默认路由选择网卡。
func DetectOn(iface string) (Net, error) {
	n := Net{}
	if validIface(iface) && iface != "auto" && iface != "lo" {
		n.Interface = iface
		n.Gateway = defaultGateway(iface, "4")
		detectV4Addrs(&n)
		n.V6 = detectV6On(iface)
	} else {
		detectByRoute(&n)
	}
	if n.Interface == "" && n.V6.Interface != "" {
		n.Interface = n.V6.Interface
	}
	if validIface(n.Interface) && n.Interface != "lo" {
		n.MAC = readMAC(n.Interface)
	}
	n.Static = n.HasV4 && !n.DHCP
	n.DNS4, n.DNS6, n.ResolvOther = readResolv()
	if !n.Present() {
		return n, fmt.Errorf("未检测到可用的网络配置")
	}
	return n, nil
}

// detectByRoute 通过默认路由探测主网卡和 IPv4 地址。
func detectByRoute(n *Net) {
	out, err := system.Output("ip", "route", "get", "1.1.1.1")
	if err == nil {
		iface, src, gw := parseRouteGet(out)
		if validIface(iface) && iface != "lo" {
			n.Interface = iface
			n.Gateway = gw
			detectV4AddrsOn(n, iface, src)
		}
	}
	n.V6 = detectV6()
}

func detectV4Addrs(n *Net) {
	detectV4AddrsOn(n, n.Interface, "")
}

func detectV4AddrsOn(n *Net, iface, src string) {
	addrs, err := listAddrs(iface, "inet")
	if err != nil || len(addrs) == 0 {
		return
	}
	prim := chooseV4(addrs, src)
	n.IP, n.Prefix = prim.IP, prim.Prefix
	n.Netmask = PrefixToMask(prim.Prefix)
	n.DHCP = prim.Dynamic
	n.HasV4 = true
	for _, a := range addrs {
		if a.CIDR() != prim.CIDR() && !a.Dynamic {
			n.Extra4 = append(n.Extra4, a.CIDR())
		}
	}
}

// detectV6 通过 IPv6 默认路由探测网卡和地址。
func detectV6() IPv6 {
	iface, src, gw := "", "", ""
	if out, err := system.Output("ip", "-6", "route", "get", "2001:4860:4860::8888"); err == nil {
		iface, src, gw = parseRouteGet(out)
	}
	if !validIface(iface) || iface == "lo" {
		iface = firstV6Iface()
		src = ""
		gw = ""
	}
	if iface == "" {
		return IPv6{}
	}
	return buildV6(iface, src, gw)
}

// detectV6On 探测指定网卡的 IPv6 配置。
func detectV6On(iface string) IPv6 {
	if !validIface(iface) || iface == "lo" {
		return IPv6{}
	}
	gw := defaultGateway(iface, "6")
	src := ""
	if out, err := system.Output("ip", "-6", "route", "get", "2001:4860:4860::8888", "dev", iface); err == nil {
		_, src, _ = parseRouteGet(out)
	}
	return buildV6(iface, src, gw)
}

// v6Mode 是 IPv6 地址的获取方式。
type v6Mode int

const (
	v6SLAACMode v6Mode = iota
	v6DHCPMode
)

// chooseV6Mode 选择 IPv6 地址的获取方式。RA 的 A 位(SLAAC)优先于
// M 位(DHCPv6)；没有 RA 标志时才参考启发式信号，信号同样 SLAAC 优先。
// 信号用函数传入，未走到对应分支时不会执行探测。
func chooseV6Mode(ra raInfo, slaacSignal, dhcpSignal func() bool) v6Mode {
	switch {
	case ra.SLAAC:
		return v6SLAACMode
	case ra.DHCPv6:
		return v6DHCPMode
	case slaacSignal():
		return v6SLAACMode
	case dhcpSignal():
		return v6DHCPMode
	}
	return v6SLAACMode
}

func buildV6(iface, src, gw string) IPv6 {
	addrs, err := listAddrs(iface, "inet6")
	if err != nil || len(addrs) == 0 {
		return IPv6{}
	}
	prim := chooseV6(addrs, src)
	v := IPv6{Interface: iface, MAC: readMAC(iface), Address: prim.IP, Prefix: prim.Prefix, Gateway: gw}

	ra := probeRA(iface)
	// 地址没有 dynamic 标记：按静态处理，并关闭 RA/autoconf，
	// 避免重启后又生成另外的地址或默认路由。
	if !prim.Dynamic {
		v.Static = true
		v.DisableRA = true
		v.DisableAutoconf = true
		// 只沿用同样是静态的额外地址，避免把 SLAAC 临时地址写回去。
		for _, a := range addrs {
			if a.CIDR() != prim.CIDR() && !a.Temporary && !a.Dynamic && !a.Mngtmp {
				v.Extra = append(v.Extra, a.CIDR())
			}
		}
		return v
	}

	if chooseV6Mode(ra,
		func() bool { return v6SLAACSignal(addrs) },
		func() bool { return v6DHCP(iface, prim.IP) }) == v6DHCPMode {
		v.DHCPv6 = true
	} else {
		v.SLAAC = true
	}
	return v
}

// v6SLAACSignal 根据隐私地址和默认路由的 proto 判断是否使用 SLAAC。
func v6SLAACSignal(addrs []Address) bool {
	for _, a := range addrs {
		if a.Temporary || a.Mngtmp {
			return true
		}
	}
	out, err := system.Output("ip", "-6", "route", "show", "default")
	return err == nil && strings.Contains(out, "proto ra")
}

// v6DHCP 判断 IPv6 地址是否来自 DHCPv6。
func v6DHCP(iface, ip string) bool {
	out, err := system.Output("ip", "-6", "route", "show", "default", "dev", iface)
	if err == nil && strings.Contains(out, "proto dhcp") {
		return true
	}
	return leaseHas(ip)
}

// leaseHas 在常见 DHCP 租约文件中查找地址，避免把 DHCPv6 误判为 SLAAC。
func leaseHas(ip string) bool {
	globs := []string{
		"/var/lib/dhcp/dhclient*.leases",
		"/var/lib/dhcpcd/*.lease",
		"/var/lib/dhcpcd/*.lease.*",
		"/var/lib/isc-dhcp/dhclient*.leases",
		"/run/systemd/netif/leases/*",
		"/var/lib/NetworkManager/*.lease",
	}
	for _, g := range globs {
		paths, _ := filepath.Glob(g)
		for _, p := range paths {
			f, err := os.Open(p)
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(io.LimitReader(f, 4<<20))
			f.Close()
			if strings.Contains(string(data), ip) {
				return true
			}
		}
	}
	return false
}

// raInfo 是 IPv6 路由器通告中的关键标志。
type raInfo struct {
	SLAAC  bool
	DHCPv6 bool
}

// probeRA 调用 rdisc6(如果已安装)读取 RA 标志。
func probeRA(iface string) raInfo {
	if !system.Exists("rdisc6") {
		return raInfo{}
	}
	out, err := system.OutputTimeout(3*time.Second, "rdisc6", "-1", iface)
	if err != nil && out == "" {
		return raInfo{}
	}
	return parseRA(out)
}

// parseRA 解析 rdisc6 的输出，读取 A 位(SLAAC)和 M 位(DHCPv6)。
// 注意 "Stateful other conf." 与地址配置无关，不能匹配。
func parseRA(out string) raInfo {
	var ra raInfo
	for line := range strings.SplitSeq(out, "\n") {
		low := strings.ToLower(line)
		switch {
		case strings.Contains(low, "autonomous address conf"):
			ra.SLAAC = strings.Contains(low, "yes")
		case strings.Contains(low, "stateful address conf"):
			ra.DHCPv6 = strings.Contains(low, "yes")
		}
	}
	return ra
}

// listAddrs 返回网卡上的全局地址(family 为 inet 或 inet6)。
func listAddrs(iface, family string) ([]Address, error) {
	out, err := system.Output("ip", "-o", "-f", family, "addr", "show", "dev", iface, "scope", "global")
	if err != nil {
		return nil, err
	}
	var addrs []Address
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for _, f := range fields {
			host, pstr, ok := strings.Cut(f, "/")
			if !ok {
				continue
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if family == "inet" && ip.To4() == nil {
				continue
			}
			if family == "inet6" && ip.To4() != nil {
				continue
			}
			p, err := strconv.Atoi(pstr)
			if err != nil || p < 1 || p > 128 || (family == "inet" && p > 32) {
				continue
			}
			addrs = append(addrs, Address{
				IP:        host,
				Prefix:    p,
				Dynamic:   slices.Contains(fields, "dynamic"),
				Temporary: slices.Contains(fields, "temporary"),
				Mngtmp:    slices.Contains(fields, "mngtmpaddr"),
				Secondary: slices.Contains(fields, "secondary"),
			})
			break
		}
	}
	return addrs, nil
}

func chooseV4(addrs []Address, src string) Address {
	for _, a := range addrs {
		if a.IP == src {
			return a
		}
	}
	for _, a := range addrs {
		if !a.Secondary {
			return a
		}
	}
	return addrs[0]
}

func chooseV6(addrs []Address, src string) Address {
	for _, a := range addrs {
		if a.IP == src && !a.Temporary {
			return a
		}
	}
	for _, a := range addrs {
		if !a.Temporary {
			return a
		}
	}
	return addrs[0]
}

func firstV6Iface() string {
	out, err := system.Output("ip", "-6", "-o", "addr", "show", "scope", "global")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSuffix(fields[1], ":")
		if validIface(name) && name != "lo" {
			return name
		}
	}
	return ""
}

// defaultGateway 返回指定网卡、指定协议的默认网关。
func defaultGateway(iface, family string) string {
	out, err := system.Output("ip", "-"+family, "route", "show", "default", "dev", iface)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "via" {
				return fields[i+1]
			}
		}
	}
	return ""
}

func readMAC(iface string) string {
	if data, err := os.ReadFile("/sys/class/net/" + iface + "/address"); err == nil {
		return strings.ToLower(strings.TrimSpace(string(data)))
	}
	out, err := system.Output("ip", "link", "show", "dev", iface)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "link/ether" {
				return strings.ToLower(fields[i+1])
			}
		}
	}
	return ""
}

func readResolv() (dns4, dns6, other []string) {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil, nil, nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "nameserver":
			if len(fields) < 2 {
				continue
			}
			s := fields[1]
			host := s
			if i := strings.IndexByte(host, '%'); i >= 0 {
				host = host[:i]
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() {
				// 127.0.0.53 之类的本地存根在新系统里并不存在。
				continue
			}
			if ip.To4() != nil {
				dns4 = appendUnique(dns4, s)
			} else {
				dns6 = appendUnique(dns6, s)
			}
		case "search", "domain", "options":
			other = append(other, line)
		}
	}
	return
}

func parseRouteGet(out string) (iface, src, gw string) {
	for _, f := range strings.Fields(out) {
		switch {
		case strings.HasPrefix(f, "dev="):
			iface = strings.TrimPrefix(f, "dev=")
		case strings.HasPrefix(f, "src="):
			src = strings.TrimPrefix(f, "src=")
		case strings.HasPrefix(f, "via="):
			gw = strings.TrimPrefix(f, "via=")
		}
	}
	fields := strings.Fields(out)
	for i := 0; i+1 < len(fields); i++ {
		switch fields[i] {
		case "via":
			gw = fields[i+1]
		case "dev":
			iface = fields[i+1]
		case "src":
			src = fields[i+1]
		}
	}
	return
}

// PrefixToMask 把 CIDR 前缀转换为点分十进制掩码。
func PrefixToMask(prefix int) string {
	if prefix <= 0 || prefix > 32 {
		return ""
	}
	m := net.CIDRMask(prefix, 32)
	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}

// MaskToPrefix 把点分十进制掩码转换为 CIDR 前缀。
func MaskToPrefix(mask string) int {
	ip := net.ParseIP(mask)
	if ip == nil {
		return 0
	}
	ones, _ := net.IPMask(ip.To4()).Size()
	return ones
}

// WidenForGateway 返回能让 gateway 与 ip 处于同一子网的前缀。
func WidenForGateway(ip, gateway string, prefix int) (int, bool) {
	a := net.ParseIP(ip).To4()
	g := net.ParseIP(gateway).To4()
	if a == nil || g == nil {
		return prefix, false
	}
	for p := prefix; p >= 8; p-- {
		n := net.IPNet{IP: a.Mask(net.CIDRMask(p, 32)), Mask: net.CIDRMask(p, 32)}
		if n.Contains(g) {
			return p, p != prefix
		}
	}
	return prefix, false
}

// validIface 只接受普通的 Linux 网卡名，避免把探测结果拼进 shell 脚本时出问题。
func validIface(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':':
		default:
			return false
		}
	}
	return true
}

func unique(in []string) []string {
	var out []string
	for _, s := range in {
		out = appendUnique(out, s)
	}
	return out
}

func appendUnique(in []string, s string) []string {
	if s == "" || slices.Contains(in, s) {
		return in
	}
	return append(in, s)
}
