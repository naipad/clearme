package cli

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/naipad/clearme/internal/app"
	"github.com/naipad/clearme/internal/netconf"
)

// btrfsCompressRe 限制 compress= 的取值，避免把任意内容写进目标系统 fstab。
var btrfsCompressRe = regexp.MustCompile(`^(zstd(:([1-9]|1[0-5]))?|zlib(:[1-9])?|lzo)$`)

// stringList 实现 flag.Value，用于可重复的 --keep-part。
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// Parse 解析命令行参数并校验，返回应用层配置。
// 只暴露生产环境常用的参数，其余按默认值处理。
func Parse(args []string) (*app.Options, error) {
	o := &app.Options{
		// 空值表示跟随当前 stable，实际代号在运行时从镜像的 Release 解析。
		Suite:        "",
		Username:     "root",
		SSHPort:      22,
		Filesystem:   "ext4",
		Timezone:     "Asia/Shanghai",
		NTP:          "time.cloudflare.com",
		DNS:          "1.1.1.1 8.8.8.8",
		Install:      "ca-certificates sudo curl wget net-tools unzip",
		Optimize:     true,
		Unattended:   true,
		InstallerSSH: true,
		LogFile:      "/var/log/clearme.log",
	}
	fs := flag.NewFlagSet("clearme", flag.ContinueOnError)
	// 解析错误由调用方统一打印一次并附上自定义 Usage()，
	// 因此关闭 flag 包自带的输出，避免重复。
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	fs.StringVar(&o.Suite, "version", "", "目标 Debian 版本：11/12/13/14/sid 或代号(默认跟随 stable)")
	fs.BoolVar(&o.ProgramVersion, "V", false, "显示 clearme 程序版本并退出")
	fs.BoolVar(&o.ProgramVersion, "program-version", false, "--V 的别名")
	fs.StringVar(&o.Mirror, "mirror", "", "镜像地址，例如 https://mirrors.aliyun.com/debian")
	fs.BoolVar(&o.China, "china", false, "使用国内镜像并自动测速")
	fs.StringVar(&o.Proxy, "proxy", "", "下载与目标系统 apt 使用的 HTTP 代理")

	fs.StringVar(&o.IP, "ip", "", "静态 IPv4 地址(留空则沿用当前 DHCP/静态方式)")
	fs.StringVar(&o.Netmask, "netmask", "", "子网掩码，例如 255.255.255.0")
	fs.StringVar(&o.Gateway, "gateway", "", "默认网关")
	fs.StringVar(&o.IPv6, "ipv6", "", "静态 IPv6 地址，例如 2001:db8::10/64(网关沿用当前 RA)")
	fs.StringVar(&o.IPv6Gateway, "ipv6-gateway", "", "IPv6 默认网关(留空沿用当前 RA/DHCPv6)")
	fs.StringVar(&o.IPv6DNS, "ipv6-dns", "", "IPv6 DNS 服务器(空格分隔)")
	fs.StringVar(&o.DNS, "dns", "1.1.1.1 8.8.8.8", "DNS 服务器(空格分隔；不指定时沿用当前系统)")
	fs.StringVar(&o.Hostname, "hostname", "", "主机名(默认沿用当前)")

	fs.StringVar(&o.Username, "user", "root", "登录用户(root 或新建用户)")
	fs.StringVar(&o.Password, "password", "", "登录密码(留空则随机生成)")
	fs.IntVar(&o.SSHPort, "ssh-port", 22, "SSH 端口")
	fs.StringVar(&o.AuthorizedKeysURL, "authorized-keys-url", "", "SSH authorized_keys 的下载地址")

	fs.StringVar(&o.Disk, "disk", "", "目标磁盘，例如 /dev/sda")
	fs.StringVar(&o.Filesystem, "filesystem", "ext4", "根文件系统：ext4/xfs/btrfs")
	fs.StringVar(&o.BtrfsCompress, "btrfs-compress", "", "btrfs 压缩算法，例如 zstd:3(需 --filesystem btrfs)")
	fs.BoolVar(&o.KeepData, "keep-data", false, "保留数据分区(自动识别)")
	var keepParts stringList
	fs.Var(&keepParts, "keep-part", "指定要保留的分区，例如 /dev/sda3(可重复)")

	fs.StringVar(&o.Install, "install", "ca-certificates sudo curl wget net-tools unzip", "额外安装的软件包(空格分隔)")
	fs.StringVar(&o.Upgrade, "upgrade", "full", "安装阶段 apt 升级：none|safe|full")
	fs.StringVar(&o.PostInstall, "post-install", "", "首次启动执行的脚本(本地路径或 URL)")
	var aptRepos stringList
	fs.Var(&aptRepos, "apt-repo", "额外的 apt 源，例如 'deb https://... trixie main'(可重复)")
	fs.BoolVar(&o.Fail2ban, "fail2ban", false, "安装 fail2ban 并启用 SSH 防爆破")
	fs.BoolVar(&o.Firmware, "firmware", false, "附带 non-free 固件")
	fs.StringVar(&o.SwapSize, "swap-size", "", "创建 swapfile，例如 512M、2G、2048MB(默认按内存自动判断)")

	fs.BoolVar(&o.DryRun, "dry-run", false, "只打印配置摘要后退出")
	fs.BoolVar(&o.Reboot, "reboot-now", false, "准备完成后立即重启")
	fs.BoolVar(&o.Yes, "yes", false, "跳过确认")
	fs.BoolVar(&o.Force, "force", false, "跳过环境检查")

	if err := fs.Parse(args); err != nil {
		return nil, localizeFlagError(err)
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "dns" {
			o.DNSExplicit = true
		}
	})
	// NTP 跟随镜像预设：国内镜像使用阿里云 NTP。
	o.NTP = defaultNTP(o)
	o.KeepParts = []string(keepParts)
	for _, r := range aptRepos {
		r = strings.TrimSpace(r)
		if !strings.HasPrefix(r, "deb ") || strings.ContainsAny(r, "\r\n") {
			return nil, fmt.Errorf("--apt-repo 格式应为 'deb URL SUITE COMPONENTS'：%q", r)
		}
		o.AptRepos = append(o.AptRepos, r)
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("未知参数 %q", fs.Arg(0))
	}
	if o.Mirror != "" {
		u, err := url.Parse(o.Mirror)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("--mirror 地址无效：%q", o.Mirror)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
		default:
			return nil, fmt.Errorf("--mirror 只支持 http/https 地址")
		}
		if strings.ContainsAny(o.Mirror, " \t\r\n\"'") {
			return nil, fmt.Errorf("--mirror 不能包含空格、引号或换行")
		}
	}
	if o.IP != "" && !isIPv4(o.IP) {
		return nil, fmt.Errorf("--ip 不是有效的 IPv4 地址：%q", o.IP)
	}
	if o.Netmask != "" {
		if o.IP == "" {
			return nil, fmt.Errorf("--netmask 需要配合 --ip 使用")
		}
		if !isNetmask(o.Netmask) {
			return nil, fmt.Errorf("--netmask 不是有效的子网掩码：%q", o.Netmask)
		}
	}
	if o.Gateway != "" && !isIPv4(o.Gateway) {
		return nil, fmt.Errorf("--gateway 不是有效的 IPv4 地址：%q", o.Gateway)
	}
	for s := range strings.FieldsSeq(o.DNS) {
		if !isIPLiteral(s) {
			return nil, fmt.Errorf("--dns 含有无效的地址：%q", s)
		}
	}
	if o.Proxy != "" {
		u, err := url.Parse(o.Proxy)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("--proxy 地址无效：%q", o.Proxy)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("--proxy 只支持 http/https/socks5/socks5h")
		}
		if strings.ContainsAny(o.Proxy, " \t\"'") {
			return nil, fmt.Errorf("--proxy 不能包含空格或引号")
		}
	}
	if o.SSHPort < 1 || o.SSHPort > 65535 {
		return nil, fmt.Errorf("--ssh-port 无效：%d", o.SSHPort)
	}
	if o.Username != "root" && !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString(o.Username) {
		return nil, fmt.Errorf("--user 无效：%q", o.Username)
	}
	switch o.Upgrade {
	case "", "none":
		o.Upgrade = ""
	case "safe":
		o.Upgrade = "safe-upgrade"
	case "full":
		o.Upgrade = "full-upgrade"
	default:
		return nil, fmt.Errorf("--upgrade 只能是 none、safe 或 full")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9 ._+:-]*$`).MatchString(o.Install) {
		return nil, fmt.Errorf("--install 含有不支持的字符")
	}
	switch o.Filesystem {
	case "ext4", "xfs", "btrfs":
	default:
		return nil, fmt.Errorf("--filesystem 只支持 ext4、xfs 或 btrfs")
	}
	if o.BtrfsCompress != "" {
		if o.Filesystem != "btrfs" {
			return nil, fmt.Errorf("--btrfs-compress 需要配合 --filesystem btrfs 使用")
		}
		if !btrfsCompressRe.MatchString(o.BtrfsCompress) {
			return nil, fmt.Errorf("--btrfs-compress 只支持 zstd[:1-15]、zlib[:1-9] 或 lzo")
		}
	}
	if o.IPv6 != "" {
		if _, _, err := netconf.ParseIPv6(o.IPv6); err != nil {
			return nil, fmt.Errorf("--ipv6 无效：%w", err)
		}
	}
	if o.IPv6Gateway != "" {
		if o.IPv6 == "" {
			return nil, fmt.Errorf("--ipv6-gateway 需要配合 --ipv6 使用")
		}
		if ip := net.ParseIP(o.IPv6Gateway); ip == nil || ip.To4() != nil {
			return nil, fmt.Errorf("--ipv6-gateway 不是有效的 IPv6 地址：%q", o.IPv6Gateway)
		}
	}
	for s := range strings.FieldsSeq(o.IPv6DNS) {
		if ip := net.ParseIP(s); ip == nil || ip.To4() != nil {
			return nil, fmt.Errorf("--ipv6-dns 不是有效的 IPv6 地址：%q", s)
		}
	}
	if o.SwapSize != "" {
		size, mb, err := parseSwapSize(o.SwapSize)
		if err != nil {
			return nil, err
		}
		o.SwapSize, o.SwapMB, o.Swap = size, mb, true
	}
	if o.KeepData && len(o.KeepParts) > 0 {
		return nil, fmt.Errorf("--keep-data 与 --keep-part 不能同时使用")
	}
	if len(o.KeepParts) > 0 {
		o.KeepData = true
	}
	return o, nil
}

// defaultNTP 在选择国内镜像时改用就近的 NTP 服务器。
func defaultNTP(o *app.Options) string {
	if o.China {
		return "ntp.aliyun.com"
	}
	return "time.cloudflare.com"
}

// parseSwapSize 解析 512M、2G、2048MB 或纯数字(MB)的 swap 大小。
func parseSwapSize(s string) (string, int, error) {
	v := strings.ToUpper(strings.TrimSpace(s))
	mult, num := 1, v
	switch {
	case strings.HasSuffix(v, "MB"):
		num = strings.TrimSuffix(v, "MB")
	case strings.HasSuffix(v, "GB"):
		num, mult = strings.TrimSuffix(v, "GB"), 1024
	case strings.HasSuffix(v, "M"):
		num = strings.TrimSuffix(v, "M")
	case strings.HasSuffix(v, "G"):
		num, mult = strings.TrimSuffix(v, "G"), 1024
	case strings.HasSuffix(v, "K"), strings.HasSuffix(v, "KB"):
		return "", 0, fmt.Errorf("--swap-size 最小单位为 MB：%q", s)
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("--swap-size 格式无效：%q", s)
	}
	if n > (1<<20)/mult {
		return "", 0, fmt.Errorf("--swap-size 需在 64M 到 1T 之间：%q", s)
	}
	mb := n * mult
	if mb < 64 {
		return "", 0, fmt.Errorf("--swap-size 需在 64M 到 1T 之间：%q", s)
	}
	if mult == 1024 {
		return fmt.Sprintf("%dG", n), mb, nil
	}
	return fmt.Sprintf("%dM", mb), mb, nil
}

// isIPv4 报告 s 是否为点分十进制的 IPv4 地址。
func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// isNetmask 报告 s 是否为连续且非全零的 IPv4 子网掩码。
func isNetmask(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return false
	}
	ones, bits := net.IPMask(ip.To4()).Size()
	return bits == 32 && ones > 0
}

// isIPLiteral 报告 s 是否为 IPv4/IPv6 地址；允许 IPv6 的 %zone 后缀。
func isIPLiteral(s string) bool {
	if i := strings.IndexByte(s, '%'); i >= 0 {
		s = s[:i]
	}
	return net.ParseIP(s) != nil
}

// localizeFlagError 把 flag 包内置的英文错误转换为中文提示。
func localizeFlagError(err error) error {
	msg := err.Error()
	if v, ok := strings.CutPrefix(msg, "flag provided but not defined: "); ok {
		return fmt.Errorf("未知参数 %s", v)
	}
	if v, ok := strings.CutPrefix(msg, "flag needs an argument: "); ok {
		return fmt.Errorf("参数 %s 缺少值", v)
	}
	if rest, ok := strings.CutPrefix(msg, `invalid value "`); ok {
		if val, tail, ok := strings.Cut(rest, `" for flag `); ok {
			name, _, _ := strings.Cut(tail, ":")
			return fmt.Errorf("参数 %s 的值 %q 无效", name, val)
		}
	}
	return err
}
