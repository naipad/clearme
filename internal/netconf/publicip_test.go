package netconf

import "testing"

// TestExtractIP 检查各种回显格式都能取出正确地址族的公网 IP。
func TestExtractIP(t *testing.T) {
	tests := []struct {
		name string
		body string
		v4   bool
		want string
	}{
		{"plain-v4", "1.2.3.4\n", true, "1.2.3.4"},
		{"ipip", "当前 IP：1.2.3.4  来自于：中国 北京", true, "1.2.3.4"},
		{"json", `ipCallback({ip:"1.2.3.4"})`, true, "1.2.3.4"},
		{"plain-v6", "2001:470:1:2::3\n", false, "2001:470:1:2::3"},
		{"private-v4", "10.0.0.5", true, ""},
		{"v4-as-v6", "1.2.3.4", false, ""},
		{"v6-as-v4", "2001:470:1:2::3", true, ""},
		{"html", "<html><body>no address</body></html>", true, ""},
	}
	for _, tt := range tests {
		if got := extractIP(tt.body, tt.v4); got != tt.want {
			t.Errorf("%s: extractIP(%q) = %q, want %q", tt.name, tt.body, got, tt.want)
		}
	}
}

// TestIsPrivateAddress 检查内网、CGNAT、回环和链路本地地址会被识别。
func TestIsPrivateAddress(t *testing.T) {
	tests := map[string]bool{
		"10.0.0.1":      true,
		"172.16.0.1":    true,
		"192.168.1.1":   true,
		"100.64.0.1":    true,
		"169.254.1.1":   true,
		"127.0.0.1":     true,
		"fd00::1":       true,
		"fe80::1":       true,
		"8.8.8.8":       false,
		"2001:470:1::1": false,
		"not-an-ip":     false,
	}
	for addr, want := range tests {
		if got := IsPrivateAddress(addr); got != want {
			t.Errorf("IsPrivateAddress(%q) = %v, want %v", addr, got, want)
		}
	}
}
