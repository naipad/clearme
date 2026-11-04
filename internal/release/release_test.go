package release

import "testing"

func TestResolve(t *testing.T) {
	cases := []struct {
		in    string
		suite string
		daily bool
	}{
		{"11", "bullseye", false},
		{"bullseye", "bullseye", false},
		{"12", "bookworm", false},
		{"13", "trixie", false},
		{"14", "forky", true},
		{"testing", "forky", true},
		{"sid", "sid", true},
		{"unstable", "sid", true},
	}
	for _, c := range cases {
		got, err := Resolve(c.in)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", c.in, err)
		}
		if got.Suite != c.suite || got.Daily != c.daily {
			t.Errorf("Resolve(%q) = %+v, want suite=%s daily=%v",
				c.in, got, c.suite, c.daily)
		}
	}
	if _, err := Resolve("10"); err == nil {
		t.Error("buster should be rejected")
	}
	if _, err := Resolve("nope"); err == nil {
		t.Error("unknown version should be rejected")
	}
	if _, err := Resolve("stable"); err == nil {
		t.Error("stable 需要联网解析，不应在 Resolve 中固定")
	}
}

// TestStableAlias 检查空值和 stable 都会跟随当前 stable。
func TestStableAlias(t *testing.T) {
	for _, v := range []string{"", "stable", "STABLE", " stable "} {
		if !StableAlias(v) {
			t.Errorf("StableAlias(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"13", "trixie", "sid", "testing"} {
		if StableAlias(v) {
			t.Errorf("StableAlias(%q) = true, want false", v)
		}
	}
}

// TestParseStable 检查从 Release 文件解析代号和主版本号。
func TestParseStable(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		suite  string
		number int
		ok     bool
	}{
		{
			name:  "current stable",
			text:  "Origin: Debian\nSuite: stable\nVersion: 13.7\nCodename: trixie\n",
			suite: "trixie", number: 13, ok: true,
		},
		{
			name:  "future stable",
			text:  "Suite: stable\nVersion: 14.2\nCodename: forky\n",
			suite: "forky", number: 14, ok: true,
		},
		{
			name:  "unknown codename",
			text:  "Version: 15.1\nCodename: duke\n",
			suite: "duke", number: 15, ok: true,
		},
		{
			name:  "known codename without version",
			text:  "Codename: trixie\n",
			suite: "trixie", number: 13, ok: true,
		},
		{
			name: "missing codename",
			text: "Suite: stable\nVersion: 13.7\n",
			ok:   false,
		},
		{
			name: "unknown codename without version",
			text: "Codename: duke\n",
			ok:   false,
		},
		{
			name: "sid is not stable",
			text: "Version: 99\nCodename: sid\n",
			ok:   false,
		},
		{
			name: "injected codename",
			text: "Version: 13.7\nCodename: trixie main\n",
			ok:   false,
		},
	}
	for _, tt := range tests {
		got, ok := ParseStable(tt.text)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v, want %v", tt.name, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Suite != tt.suite || got.Number != tt.number {
			t.Errorf("%s: got %+v, want suite=%s number=%d", tt.name, got, tt.suite, tt.number)
		}
		if got.Daily {
			t.Errorf("%s: stable 不应使用每日构建", tt.name)
		}
	}
}

func TestSecurityRepo(t *testing.T) {
	got := SecurityRepo("https", "ftp.debian.org", "/debian/", "trixie", false)
	if want := "https://deb.debian.org/debian-security"; got != want {
		t.Errorf("SecurityRepo = %q, want %q", got, want)
	}
	if got := SecurityRepo("https", "deb.debian.org", "/debian", "sid", true); got != "" {
		t.Errorf("sid should have no security repo, got %q", got)
	}
	if got := SecurityRepo("https", "deb.debian.org", "/debian", "bookworm", false); got != "https://deb.debian.org/debian-security" {
		t.Errorf("unexpected repo %q", got)
	}
}
