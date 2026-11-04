package netconf

import (
	"fmt"
	"net"
)

// ParseIPv6 解析 --ipv6 的值，支持“地址”或“地址/前缀”两种写法。
// 未写前缀时默认 64；IPv4 地址会被拒绝。
func ParseIPv6(s string) (address string, prefix int, err error) {
	if ip, ipnet, cerr := net.ParseCIDR(s); cerr == nil {
		if ip.To4() != nil {
			return "", 0, fmt.Errorf("%q 是 IPv4 地址", s)
		}
		ones, _ := ipnet.Mask.Size()
		return ip.String(), ones, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return "", 0, fmt.Errorf("%q 不是合法的 IPv6 地址", s)
	}
	if ip.To4() != nil {
		return "", 0, fmt.Errorf("%q 是 IPv4 地址", s)
	}
	return ip.String(), 64, nil
}
