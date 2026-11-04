package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/naipad/clearme/internal/logx"
	"github.com/naipad/clearme/internal/mirror"
	"github.com/naipad/clearme/internal/netconf"
	"github.com/naipad/clearme/internal/release"
	"github.com/naipad/clearme/internal/ui"
	"github.com/naipad/clearme/internal/version"
)

// summaryKeyWidth 是摘要键名的对齐宽度，与正常安装模式保持一致。
const summaryKeyWidth = logx.KeyWidth

// summaryRow 是一行配置摘要。
type summaryRow struct {
	key   string
	value string
}

// summaryInput 收集生成摘要所需的全部信息。
type summaryInput struct {
	o           *Options
	suite, arch string

	disk     string
	diskNote string // dry-run 下磁盘只是示例值时的提示
	efi      bool

	n        netconf.Net
	hostname string

	mirrorURL    string
	securityRepo string

	keep   []int
	mounts map[int]string // 分区号 -> 挂载点；不挂载的分区不出现

	password    string
	random      bool
	keepExample bool // dry-run 下标注保留分区为示例

	// 登录地址：内网地址会优先替换成探测到的公网 IP。
	loginV4 string
	loginV6 string
}

// rows 按“次要 -> 重要”的顺序生成配置摘要行：
// 更新与软件源在前，时区、网络、目标系统放在最后，
// 这样命令结束后最靠近提示符的就是最需要核对的信息。
// 主机名、登录用户和密码单独放在登录信息区块里。
func (in summaryInput) rows() []summaryRow {
	o := in.o

	var rows []summaryRow
	update := "关"
	if o.Unattended {
		update = "unattended-upgrades (安全更新)"
	}
	rows = append(rows, summaryRow{"自动更新", update})
	if o.Fail2ban {
		rows = append(rows, summaryRow{"防护", "fail2ban (SSH 防爆破)"})
	}

	security := in.securityRepo
	if security == "" {
		security = "无 (每日构建或 sid)"
	}
	rows = append(rows,
		summaryRow{"软件源", in.mirrorURL},
		summaryRow{"安全源", security},
	)
	if len(o.AptRepos) > 0 {
		rows = append(rows, summaryRow{"额外源", fmt.Sprintf("%d 个自定义源", len(o.AptRepos))})
	}

	rows = append(rows, summaryRow{"时区", fmt.Sprintf("%s · NTP %s", o.Timezone, o.NTP)})
	if nic := nicText(in.n); nic != "" {
		rows = append(rows, summaryRow{"网卡", nic})
	}
	rows = append(rows,
		summaryRow{"IPv4", ipv4Text(in.n)},
		summaryRow{"IPv6", ipv6Text(in.n)},
		summaryRow{"DNS", dnsText(in.n)},
	)

	if o.Proxy != "" {
		rows = append(rows, summaryRow{"代理", proxyText(o.Proxy)})
	}
	if o.PostInstall != "" {
		rows = append(rows, summaryRow{"安装后脚本", o.PostInstall})
	}
	if len(in.keep) > 0 {
		rows = append(rows, summaryRow{"保留分区", keepText(in.disk, in.keep, in.mounts, in.keepExample)})
	}
	rows = append(rows, summaryRow{"目标", targetText(o, in)})
	return rows
}

// loginRows 返回登录信息区块：主机名、登录地址、用户和密码。
func (in summaryInput) loginRows() []summaryRow {
	o := in.o
	user := o.Username
	if o.AuthorizedKeysURL != "" {
		user += " · 公钥登录"
	}
	return []summaryRow{
		{"主机名称", in.hostname},
		{"登录地址", loginAddressText(in.loginV4, in.loginV6, o.SSHPort)},
		{"登录用户", user},
		{"登录密码", passwordText(in.password, in.random)},
	}
}

// loginAddresses 返回登录用的地址。内网地址(含 CGNAT)会先查询公网 IP，
// 查询失败或本来就是公网地址时直接用探测到的地址。
func loginAddresses(n netconf.Net) (v4, v6 string) {
	if n.HasV4 {
		v4 = n.IP
	}
	if n.V6.Present() {
		v6 = n.V6.Address
	}
	needV4 := v4 != "" && netconf.IsPrivateAddress(v4)
	needV6 := v6 != "" && netconf.IsPrivateAddress(v6)
	if !needV4 && !needV6 {
		return v4, v6
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	if needV4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pub := netconf.PublicIP(ctx, true); pub != "" {
				v4 = pub
			}
		}()
	}
	if needV6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pub := netconf.PublicIP(ctx, false); pub != "" {
				v6 = pub
			}
		}()
	}
	wg.Wait()
	return v4, v6
}

// loginAddressText 生成登录地址：优先 IPv4，同时有 IPv6 时一并列出。
// IPv6 按 URL 习惯放在方括号里，端口在括号外。
func loginAddressText(v4, v6 string, port int) string {
	var parts []string
	if v4 != "" {
		parts = append(parts, fmt.Sprintf("%s:%d", v4, port))
	}
	if v6 != "" {
		parts = append(parts, fmt.Sprintf("[%s]:%d", v6, port))
	}
	if len(parts) == 0 {
		return "未检测到可用地址"
	}
	return strings.Join(parts, " · ")
}

// proxyText 隐藏代理 URL 中的用户名和密码，避免写入日志或终端历史。
func proxyText(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("redacted")
	return u.String()
}

// targetText 把引导方式、磁盘和文件系统合并成一行。
// 系统版本、架构和固件状态放在摘要标题里。
func targetText(o *Options, in summaryInput) string {
	boot := "BIOS"
	if in.efi {
		boot = fmt.Sprintf("UEFI (ESP %d MB)", espSizeMB)
	}
	filesystem := o.Filesystem
	if o.BtrfsCompress != "" {
		filesystem += ", compress=" + o.BtrfsCompress
	}
	disk := in.disk + " (" + filesystem
	if in.diskNote != "" {
		disk += ", " + in.diskNote
	}
	disk += ")"
	return boot + " · " + disk
}

// nicText 返回网卡和 MAC；IPv4/IPv6 不在同一块网卡时两块都列出。
// 例如 “eth0 (00:16:3e:4f:76:5b)”。
func nicText(n netconf.Net) string {
	var parts []string
	add := func(iface, mac string) {
		if iface == "" {
			return
		}
		s := iface
		if mac != "" {
			s += " (" + mac + ")"
		}
		parts = append(parts, s)
	}
	add(n.Interface, n.MAC)
	if n.V6.Interface != "" && n.V6.Interface != n.Interface {
		add(n.V6.Interface, n.V6.MAC)
	}
	return strings.Join(parts, " · ")
}

// ipv4Text 返回 IPv4 的展示文本，例如
// “静态 192.0.2.10/24 → 192.0.2.1”或“DHCP 10.0.0.8/24 → 10.0.0.1”。
func ipv4Text(n netconf.Net) string {
	switch {
	case n.HasV4 && n.IP != "":
		label := "DHCP"
		if n.Static {
			label = "静态"
		}
		s := label + " " + n.IP
		if n.Prefix > 0 {
			s += fmt.Sprintf("/%d", n.Prefix)
		}
		if n.Gateway != "" {
			s += " → " + n.Gateway
		}
		return s
	case n.HasV4:
		return "DHCP"
	default:
		return "无"
	}
}

// ipv6Text 返回 IPv6 的展示文本，格式与 IPv4 一致，例如
// “静态 2001:db8::10/64 → 2001:db8::1”。
func ipv6Text(n netconf.Net) string {
	if !n.V6.Present() {
		return "无"
	}
	var parts []string
	label := "SLAAC"
	switch {
	case n.V6.Static:
		label = "静态"
	case n.V6.DHCPv6:
		label = "DHCPv6"
	}
	s := label + " " + n.V6.Address
	if n.V6.Prefix > 0 {
		s += fmt.Sprintf("/%d", n.V6.Prefix)
	}
	if n.V6.Gateway != "" {
		s += " → " + n.V6.Gateway
	}
	parts = append(parts, s)
	if len(n.V6.Extra) > 0 {
		parts = append(parts, fmt.Sprintf("另 %d 个地址", len(n.V6.Extra)))
	}
	return strings.Join(parts, " · ")
}

// dnsText 返回目标系统使用的 DNS。
func dnsText(n netconf.Net) string {
	if dns := n.DNS(); len(dns) > 0 {
		return strings.Join(dns, " ")
	}
	if n.HasV4 || n.V6.Present() {
		return "由 DHCP/RA 下发"
	}
	return "无"
}

// keepText 生成保留分区的展示文本；只有可挂载分区会显示挂载点。
func keepText(disk string, keep []int, mounts map[int]string, example bool) string {
	base := strings.TrimPrefix(disk, "/dev/")
	parts := make([]string, 0, len(keep))
	for _, num := range keep {
		p := fmt.Sprintf("%s%d", base, num)
		if mount := mounts[num]; mount != "" {
			p += " → " + mount
		}
		parts = append(parts, p)
	}
	s := strings.Join(parts, "，")
	if example {
		s += " (示例, 运行时自动识别)"
	}
	return s
}

// passwordText 返回登录密码的展示文本；随机密码会附带提示。
func passwordText(password string, random bool) string {
	switch {
	case random && password != "":
		return password + "    ← 随机生成，请立即保存"
	case password != "":
		return password
	default:
		return "未指定 (实际运行时随机生成并显示一次)"
	}
}

// sectionRule 生成“──── 标题 ────”形式的分节横线。
func sectionRule(title string) string {
	return strings.Repeat("─", 16) + " " + title + " " + strings.Repeat("─", 16)
}

func loginTopRule() string { return sectionRule("登录信息") }

func summaryBottomRule() string {
	return strings.Repeat("─", 42)
}

// printSummary 打印最终配置；随机密码只在这里出现一次。
func printSummary(o *Options, log *logx.Logger, suite, arch, disk string, n netconf.Net, password string, random bool, keep []int, mounts map[int]string, mirrorURL, securityRepo string) {
	loginV4, loginV6 := loginAddresses(n)
	in := summaryInput{
		o: o, suite: suite, arch: arch,
		disk: disk, efi: detectEFI(),
		n: n, hostname: resolveHostname(o),
		mirrorURL: mirrorURL, securityRepo: securityRepo,
		keep: keep, mounts: mounts,
		password: password, random: random,
		loginV4: loginV4, loginV6: loginV6,
	}

	log.Title("%s", sectionRule(summaryTitle(o, in)))
	for _, r := range in.rows() {
		log.Summary(r.key, "%s", r.value)
	}
	log.Title("%s", loginTopRule())
	for _, r := range in.loginRows() {
		if r.key == "登录密码" && random {
			log.SuccessSummary(r.key, "%s", r.value)
		} else {
			log.Summary(r.key, "%s", r.value)
		}
	}
	log.Title("%s", summaryBottomRule())
}

// printDryRunSummary 打印 --dry-run 的配置概览。
func printDryRunSummary(o *Options, suite, arch, disk, diskNote, suiteNote string, efi bool, n netconf.Net, hostname string, m mirror.Mirror, securityRepo string, keep []int, mounts map[int]string) {
	loginV4, loginV6 := loginAddresses(n)
	in := summaryInput{
		o: o, suite: suite, arch: arch,
		disk: disk, diskNote: diskNote, efi: efi,
		n: n, hostname: hostname,
		mirrorURL: m.Base(), securityRepo: securityRepo,
		keep: keep, mounts: mounts,
		password: o.Password, random: o.Password == "",
		keepExample: true,
		loginV4:     loginV4, loginV6: loginV6,
	}
	rows := in.rows()
	login := in.loginRows()

	width := summaryKeyWidth
	for _, r := range rows {
		if w := ui.Width(r.key); w > width {
			width = w
		}
	}
	for _, r := range login {
		if w := ui.Width(r.key); w > width {
			width = w
		}
	}

	fmt.Fprintf(ui.Stdout(), "\n%s %s\n", ui.Title("clearme "+version.Version), ui.Dim("· dry-run"))
	fmt.Fprintf(ui.Stdout(), "%s\n", ui.Dim("仅渲染配置，不会下载文件或修改系统。"))
	if suiteNote != "" {
		fmt.Fprintf(ui.Stdout(), "%s\n", ui.Yellow(suiteNote))
	}
	fmt.Fprintln(ui.Stdout())
	ui.Titlef("%s", sectionRule(summaryTitle(o, in)))
	for _, r := range rows {
		fmt.Fprintf(ui.Stdout(), "%s  %s\n", ui.Cyan(ui.Pad(r.key, width)), r.value)
	}
	ui.Titlef("%s", loginTopRule())
	for _, r := range login {
		fmt.Fprintf(ui.Stdout(), "%s  %s\n", ui.Cyan(ui.Pad(r.key, width)), r.value)
	}
	ui.Titlef("%s", summaryBottomRule())
}

// summaryTitle 返回摘要标题，例如 “Debian 13 (trixie, amd64, firmware)”。
// 启用 --firmware 时在架构后面加上 firmware。
func summaryTitle(o *Options, in summaryInput) string {
	extra := ""
	if o.Firmware {
		extra = ", firmware"
	}
	if display := release.Display(in.suite); display != in.suite {
		return fmt.Sprintf("Debian %s (%s, %s%s)", display, in.suite, in.arch, extra)
	}
	return fmt.Sprintf("Debian %s (%s%s)", in.suite, in.arch, extra)
}
