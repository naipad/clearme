package netconf

import "testing"

func TestParseIPv6(t *testing.T) {
	tests := []struct {
		in     string
		addr   string
		prefix int
		ok     bool
	}{
		{"2001:db8::10/64", "2001:db8::10", 64, true},
		{"2001:db8::10", "2001:db8::10", 64, true},
		{"2001:db8::10/128", "2001:db8::10", 128, true},
		{"192.0.2.1", "", 0, false},
		{"192.0.2.1/24", "", 0, false},
		{"not-an-ip", "", 0, false},
	}
	for _, tt := range tests {
		addr, prefix, err := ParseIPv6(tt.in)
		if tt.ok && err != nil {
			t.Errorf("ParseIPv6(%q) error = %v", tt.in, err)
			continue
		}
		if !tt.ok && err == nil {
			t.Errorf("ParseIPv6(%q) 应当失败", tt.in)
			continue
		}
		if tt.ok && (addr != tt.addr || prefix != tt.prefix) {
			t.Errorf("ParseIPv6(%q) = %s/%d, want %s/%d", tt.in, addr, prefix, tt.addr, tt.prefix)
		}
	}
}
