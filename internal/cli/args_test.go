package cli

import (
	"testing"

	"github.com/naipad/clearme/internal/app"
)

// TestDefaultNTP 检查默认与 --china 时的 NTP 选择。
func TestDefaultNTP(t *testing.T) {
	tests := []struct {
		name string
		o    app.Options
		want string
	}{
		{"默认", app.Options{}, "time.cloudflare.com"},
		{"china", app.Options{China: true}, "ntp.aliyun.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultNTP(&tt.o); got != tt.want {
				t.Errorf("defaultNTP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseDefaultUpgrade 检查默认在首次启动执行全量升级。
func TestParseDefaultUpgrade(t *testing.T) {
	o, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Upgrade != "full-upgrade" {
		t.Errorf("默认 Upgrade = %q, want %q", o.Upgrade, "full-upgrade")
	}
	o, err = Parse([]string{"--upgrade", "none"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Upgrade != "" {
		t.Errorf("--upgrade none 时 Upgrade = %q, want 空", o.Upgrade)
	}
}

// TestParseDefaults 检查精简后仍保留的生产默认值。
func TestParseDefaults(t *testing.T) {
	o, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Unattended || !o.Optimize || !o.InstallerSSH {
		t.Errorf("默认开关错误：unattended=%v optimize=%v installer-ssh=%v",
			o.Unattended, o.Optimize, o.InstallerSSH)
	}
	// 空值表示跟随 stable，运行时再从镜像解析具体代号。
	if o.Suite != "" || o.Filesystem != "ext4" || o.Timezone != "Asia/Shanghai" {
		t.Errorf("默认值错误：%+v", o)
	}
	if o.NTP != "time.cloudflare.com" {
		t.Errorf("默认 NTP = %q", o.NTP)
	}
}

// TestParseNewOptions 覆盖保留参数的解析。
func TestParseNewOptions(t *testing.T) {
	o, err := Parse([]string{
		"--version", "12",
		"--proxy", "http://127.0.0.1:3128",
		"--mirror", "https://mirror.example/debian",
		"--ip", "192.0.2.10",
		"--netmask", "255.255.255.0",
		"--gateway", "192.0.2.1",
		"--dns", "9.9.9.9 2620:fe::fe",
		"--filesystem", "xfs",
		"--swap-size", "2G",
		"--ipv6", "2001:db8::10/64",
		"--ipv6-gateway", "fe80::1",
		"--ipv6-dns", "2001:4860:4860::8888",
		"--install", "nginx git",
		"--upgrade", "safe",
		"--post-install", "/tmp/post.sh",
		"--apt-repo", "deb https://example.com/debian bookworm main",
		"--fail2ban",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Suite != "12" {
		t.Errorf("Suite = %q, want 12", o.Suite)
	}
	if o.Proxy != "http://127.0.0.1:3128" {
		t.Errorf("Proxy = %q", o.Proxy)
	}
	if o.Mirror != "https://mirror.example/debian" || o.IP != "192.0.2.10" ||
		o.Netmask != "255.255.255.0" || o.Gateway != "192.0.2.1" {
		t.Errorf("镜像/IPv4 参数解析错误：%+v", o)
	}
	if o.DNS != "9.9.9.9 2620:fe::fe" || !o.DNSExplicit {
		t.Errorf("DNS 参数解析错误：dns=%q explicit=%v", o.DNS, o.DNSExplicit)
	}
	if o.Filesystem != "xfs" || o.IPv6 != "2001:db8::10/64" ||
		o.IPv6Gateway != "fe80::1" || o.IPv6DNS != "2001:4860:4860::8888" {
		t.Errorf("磁盘/网络参数解析错误：%+v", o)
	}
	if o.SwapSize != "2G" || o.SwapMB != 2048 || !o.Swap {
		t.Errorf("swap 参数解析错误：size=%q mb=%d swap=%v", o.SwapSize, o.SwapMB, o.Swap)
	}
	if o.Install != "nginx git" || o.Upgrade != "safe-upgrade" {
		t.Errorf("安装/升级参数解析错误：install=%q upgrade=%q", o.Install, o.Upgrade)
	}
	if o.PostInstall != "/tmp/post.sh" || len(o.AptRepos) != 1 || !o.Fail2ban {
		t.Errorf("扩展参数解析错误：%+v", o)
	}
}

// TestParseBtrfsCompress 检查 btrfs 压缩参数只接受受支持的算法和级别。
func TestParseBtrfsCompress(t *testing.T) {
	for _, algo := range []string{"zstd", "zstd:1", "zstd:15", "zlib", "zlib:9", "lzo"} {
		o, err := Parse([]string{"--filesystem", "btrfs", "--btrfs-compress", algo})
		if err != nil {
			t.Errorf("Parse(--btrfs-compress %q) error = %v", algo, err)
			continue
		}
		if o.Filesystem != "btrfs" || o.BtrfsCompress != algo {
			t.Errorf("解析结果错误：filesystem=%q compress=%q", o.Filesystem, o.BtrfsCompress)
		}
	}
	for _, args := range [][]string{
		{"--btrfs-compress", "zstd:3"}, // 未指定 btrfs
		{"--filesystem", "ext4", "--btrfs-compress", "zstd:3"},
		{"--filesystem", "btrfs", "--btrfs-compress", "gzip"},
		{"--filesystem", "btrfs", "--btrfs-compress", "zstd:16"},
		{"--filesystem", "btrfs", "--btrfs-compress", "zlib:10"},
		{"--filesystem", "btrfs", "--btrfs-compress", "zstd:3;rm"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) 应当失败", args)
		}
	}
}

// TestParseSwapSize 检查 swap 大小的单位换算与范围校验。
func TestParseSwapSize(t *testing.T) {
	ok := []struct {
		in     string
		want   string
		wantMB int
	}{
		{"512M", "512M", 512},
		{"2G", "2G", 2048},
		{"2g", "2G", 2048},
		{"2048", "2048M", 2048},
		{"2048MB", "2048M", 2048},
		{"2GB", "2G", 2048},
	}
	for _, tt := range ok {
		got, mb, err := parseSwapSize(tt.in)
		if err != nil {
			t.Errorf("parseSwapSize(%q) error = %v", tt.in, err)
			continue
		}
		if got != tt.want || mb != tt.wantMB {
			t.Errorf("parseSwapSize(%q) = %q/%d, want %q/%d", tt.in, got, mb, tt.want, tt.wantMB)
		}
	}
	for _, in := range []string{"", "2X", "32M", "1T", "0", "512KB"} {
		if _, _, err := parseSwapSize(in); err == nil {
			t.Errorf("parseSwapSize(%q) 应当失败", in)
		}
	}
}

// TestParseNewOptionErrors 检查保留参数的格式与冲突校验。
func TestParseNewOptionErrors(t *testing.T) {
	tests := [][]string{
		{"--proxy", "ftp://proxy:21"},
		{"--apt-repo", "https://example.com/debian bookworm main"},
		{"--upgrade", "bad"},
		{"--install", "bad;rm"},
		{"--ssh-port", "0"},
		{"--ip", "300.1.1.1"},
		{"--ip", "192.0.2.10", "--netmask", "255.0.255.0"},
		{"--netmask", "255.255.255.0"},
		{"--gateway", "not-an-ip"},
		{"--dns", "1.1.1.1 bad"},
		{"--mirror", "ftp://mirror.example/debian"},
		{"--mirror", "https://"},
		{"--mirror", "https://mirror.example/debian extra"},
		{"--filesystem", "zfs"},
		{"--swap-size", "2X"},
		{"--swap-size", "32M"},
		{"--ipv6", "not-an-ip"},
		{"--ipv6-gateway", "fe80::1"},
		{"--ipv6", "2001:db8::10", "--ipv6-gateway", "192.0.2.1"},
		{"--ipv6-dns", "1.1.1.1"},
		{"--keep-data", "--keep-part", "/dev/sda3"},
	}
	for _, args := range tests {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) 应当失败", args)
		}
	}
}

// TestTrimmedFlagsRejected 检查精简掉的参数会报未知参数。
func TestTrimmedFlagsRejected(t *testing.T) {
	tests := [][]string{
		{"--arch", "amd64"},
		{"--debian", "12"},
		{"--dhcp"},
		{"--installer-ssh"},
		{"--no-installer-ssh"},
		{"--efi"},
		{"--esp", "512"},
		{"--ntp", "pool.example"},
		{"--swap"},
		{"--loader"},
	}
	for _, args := range tests {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) 应当报未知参数", args)
		}
	}
}

// TestParseFail2banDefault 检查 fail2ban 默认关闭，需显式开启。
func TestParseFail2banDefault(t *testing.T) {
	o, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Fail2ban {
		t.Error("fail2ban 应默认关闭")
	}
}
