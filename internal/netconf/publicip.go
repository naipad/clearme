package netconf

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// publicIPV4 与 publicIPV6 是按可达性排列的公网 IP 回显服务。
// 只读取很小的纯文本响应；不经过 --proxy，否则拿到的是代理的地址。
var publicIPEndpoints = map[bool][]string{
	true: {
		"https://api.ipify.org",
		"https://myip.ipip.net",
		"https://ip.3322.net",
		"https://ipv4.icanhazip.com",
		"https://www.taobao.com/help/getip.php",
	},
	false: {
		"https://api6.ipify.org",
		"https://ipv6.icanhazip.com",
		"https://test-ipv6.com/ip/",
	},
}

// PublicIP 通过公网 IP 回显服务查询本机的公网地址。
// v4 为 true 时查询 IPv4，否则查询 IPv6；失败返回空字符串，
// 调用方应回退到本地探测到的地址。
func PublicIP(ctx context.Context, v4 bool) string {
	client := &http.Client{
		Timeout: 3 * time.Second,
		// 直连：走系统代理会把代理出口地址当成这台机器的公网 IP。
		Transport: &http.Transport{Proxy: nil},
	}
	for _, endpoint := range publicIPEndpoints[v4] {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "clearme")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		if ip := extractIP(string(body), v4); ip != "" {
			return ip
		}
	}
	return ""
}

// extractIP 从回显服务的响应里取出第一个指定地址族的公网地址。
// 兼容纯文本、JSON、中文(如“当前 IP：1.2.3.4”)等格式。
func extractIP(body string, v4 bool) string {
	fields := strings.FieldsFunc(body, func(r rune) bool {
		return !unicode.IsDigit(r) && !strings.ContainsRune("abcdefABCDEF:.", r)
	})
	for _, f := range fields {
		f = strings.Trim(f, ".:")
		ip := net.ParseIP(f)
		if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() ||
			ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if v4 {
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
			continue
		}
		if ip.To4() == nil {
			return ip.String()
		}
	}
	return ""
}

// IsPrivateAddress 判断地址是否不是从公网直接可达(私有网段、CGNAT、
// 回环、链路本地)。这类地址作为登录地址通常需要改用公网 IP。
func IsPrivateAddress(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	// 100.64.0.0/10(运营商级 NAT)不属于私有网段，但同样不可从公网直达。
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}
	return false
}
