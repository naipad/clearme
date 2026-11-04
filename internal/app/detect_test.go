package app

import "testing"

// TestSanitizeHostname 检查非法字符会被替换，空结果回退到 localhost。
func TestSanitizeHostname(t *testing.T) {
	tests := map[string]string{
		"Web_01.Example": "web-01-example",
		"-":              "localhost",
		"":               "localhost",
		"中文":             "localhost",
	}
	for in, want := range tests {
		if got := sanitizeHostname(in); got != want {
			t.Errorf("sanitizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
}
