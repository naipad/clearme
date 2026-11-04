package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naipad/clearme/internal/keepdata"
	"github.com/naipad/clearme/internal/logx"
	"github.com/naipad/clearme/internal/mirror"
	"github.com/naipad/clearme/internal/netconf"
	"github.com/naipad/clearme/internal/system"
)

// resolveHostname 返回清理后的主机名：优先使用参数，否则取当前主机名。
func resolveHostname(o *Options) string {
	h := o.Hostname
	if h == "" {
		h, _ = os.Hostname()
	}
	return sanitizeHostname(h)
}

// sanitizeHostname 把任意字符串转换为合法的短主机名。
func sanitizeHostname(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = out[:63]
	}
	if out == "" {
		return "localhost"
	}
	return out
}

// targetArch 从当前系统推断目标架构。
func targetArch() (string, error) {
	out, err := system.Output("dpkg", "--print-architecture")
	if err == nil && out != "" {
		return out, nil
	}
	out, err = system.Output("uname", "-m")
	if err != nil {
		return "", fmt.Errorf("无法检测系统架构：%w", err)
	}
	switch out {
	case "x86_64":
		return "amd64", nil
	case "aarch64":
		return "arm64", nil
	case "i686", "i386":
		return "i386", nil
	}
	return "", fmt.Errorf("不支持的架构 %q", out)
}

// resolveNetwork 决定目标系统的网络配置：
//   - 没有 --ip 时沿用当前系统的 DHCP/静态方式；
//   - --ip/--netmask/--gateway 显式指定静态 IPv4；
//   - IPv6 沿用探测结果(SLAAC、DHCPv6 或静态)。
//
// 同时返回渲染 /etc/network/interfaces 所需的选项。
func resolveNetwork(o *Options, releaseNo int, log *logx.Logger) (netconf.Net, netconf.RenderOptions, error) {
	n, derr := netconf.DetectOn("auto")
	if derr != nil && o.IP == "" && o.IPv6 == "" {
		return n, netconf.RenderOptions{}, derr
	}

	switch {
	case o.IP != "":
		if n.Interface == "" {
			return n, netconf.RenderOptions{}, fmt.Errorf("指定 --ip 时未检测到可用网卡；请检查网络连接")
		}
		n.IP = o.IP
		n.Gateway = o.Gateway
		n.HasV4 = true
		n.DHCP = false
		n.Static = true
		n.Prefix = 24
		if o.Netmask != "" {
			n.Prefix = netconf.MaskToPrefix(o.Netmask)
		}
	case n.HasV4 && o.Gateway != "":
		n.Gateway = o.Gateway
	}

	if n.Static && n.HasV4 {
		if o.IP != "" && o.Netmask != "" {
			n.Netmask = o.Netmask
		}
		if warn := applyGatewayWidening(&n, n.Prefix); warn != "" && log != nil {
			log.Warn("%s", warn)
		}
	}
	if !n.HasV4 && log != nil {
		log.Warn("未检测到 IPv4 配置；目标系统不会写入 IPv4 stanza")
	}

	// --ipv6 显式覆盖探测结果，作为静态地址写入目标系统。
	if o.IPv6 != "" {
		addr, prefix, err := netconf.ParseIPv6(o.IPv6)
		if err != nil {
			return n, netconf.RenderOptions{}, err
		}
		if n.Interface == "" {
			return n, netconf.RenderOptions{}, fmt.Errorf("指定 --ipv6 时未检测到网卡；请检查网络连接")
		}
		gateway := o.IPv6Gateway
		if gateway == "" {
			// 未显式指定时沿用当前 RA/DHCPv6 探测到的网关。
			gateway = n.V6.Gateway
		}
		n.V6 = netconf.IPv6{
			Interface:       n.Interface,
			Address:         addr,
			Prefix:          prefix,
			Gateway:         gateway,
			Static:          true,
			DisableRA:       true,
			DisableAutoconf: true,
		}
	}

	opts := netconf.RenderOptions{Release: releaseNo}
	applyDNS(o, &n, &opts)
	return n, opts, nil
}

// applyDNS 计算目标系统使用的 DNS。
// 用户显式给出 --dns 时以用户为准；否则静态地址优先沿用当前 resolv.conf，
// 为空时退回到公共 DNS，动态地址则交给 DHCP/RA 下发。
func applyDNS(o *Options, n *netconf.Net, opts *netconf.RenderOptions) {
	if o.DNSExplicit {
		v4, v6 := splitDNS(o.DNS)
		if o.IPv6DNS != "" {
			_, v6 = splitDNS(o.IPv6DNS)
		}
		n.DNS4, n.DNS6 = v4, v6
		opts.DNS4, opts.DNS6 = v4, v6
		opts.WriteDNS4 = n.HasV4 && len(v4) > 0
		opts.WriteDNS6 = n.V6.Present() && len(v6) > 0
		return
	}
	// --ipv6-dns 只覆盖 IPv6 部分，IPv4 DNS 仍按原逻辑探测。
	if o.IPv6DNS != "" {
		_, v6 := splitDNS(o.IPv6DNS)
		n.DNS6 = v6
		opts.DNS6 = v6
		opts.WriteDNS6 = n.V6.Present() && len(v6) > 0
	}

	fallback4, fallback6 := netconf.FallbackDNS(o.China)
	if n.Static && n.HasV4 {
		if len(n.DNS4) == 0 {
			n.DNS4 = fallback4
		}
		opts.DNS4 = n.DNS4
		opts.WriteDNS4 = len(n.DNS4) > 0
	}
	if n.V6.Static {
		if len(n.DNS6) == 0 {
			n.DNS6 = fallback6
		}
		opts.DNS6 = n.DNS6
		opts.WriteDNS6 = len(n.DNS6) > 0
	}
	// 动态地址不写死 DNS：DHCP/DHCPv6/RA 会下发。
	opts.DNS4 = n.DNS4
	opts.DNS6 = n.DNS6
}

// splitDNS 按地址族拆分空格分隔的 DNS 列表。
func splitDNS(s string) (v4, v6 []string) {
	for f := range strings.FieldsSeq(s) {
		host := f
		if i := strings.IndexByte(host, '%'); i >= 0 {
			host = host[:i]
		}
		ip := net.ParseIP(host)
		switch {
		case ip == nil:
			continue
		case ip.To4() != nil:
			v4 = append(v4, f)
		default:
			v6 = append(v6, f)
		}
	}
	return v4, v6
}

// renderNetwork 生成要写入目标系统的 /etc/network/interfaces 和 resolv.conf。
func renderNetwork(n netconf.Net, opts netconf.RenderOptions) (interfaces, resolvConf string) {
	if !n.Present() {
		return "", ""
	}
	interfaces = netconf.RenderInterfaces(n, opts)
	if dns := n.DNS(); len(dns) > 0 {
		resolvConf = netconf.RenderResolvConf(dns, n.ResolvOther)
	}
	return interfaces, resolvConf
}

// applyGatewayWidening 补全 n.Prefix/Netmask；如果网关不在子网内，
// 会放宽掩码并返回警告文本。
func applyGatewayWidening(n *netconf.Net, prefix int) string {
	if n.IP == "" || n.Gateway == "" {
		n.Prefix = prefix
		n.Netmask = netconf.PrefixToMask(prefix)
		return ""
	}
	if p, changed := netconf.WidenForGateway(n.IP, n.Gateway, prefix); changed {
		n.Prefix = p
		n.Netmask = netconf.PrefixToMask(p)
		return fmt.Sprintf("网关 %s 不在子网内；已把掩码放宽为 %s", n.Gateway, n.Netmask)
	}
	n.Prefix = prefix
	n.Netmask = netconf.PrefixToMask(prefix)
	return ""
}

// rootDevice 返回挂载在 / 的设备路径。
func rootDevice() (string, error) {
	src, err := system.Output("findmnt", "-n", "-o", "SOURCE", "/")
	if err != nil || src == "" {
		return "", fmt.Errorf("无法确定根分区所在磁盘；请用 --disk /dev/sdX 指定")
	}
	// btrfs 子卷会显示为 /dev/sda2[/@root]，去掉括号部分。
	if i := strings.IndexByte(src, '['); i > 0 {
		src = src[:i]
	}
	return src, nil
}

// rootDisks 返回根文件系统所在的底层磁盘。
// 使用 lsblk --inverse，支持根分区位于 LVM、软 RAID 或跨盘 btrfs 的情况。
func rootDisks(src string) ([]string, error) {
	out, err := system.Output("lsblk", "-p", "-rn", "--inverse", "-o", "NAME,TYPE", src)
	if err != nil {
		return nil, system.ErrCommand("lsblk", err, out)
	}
	seen := map[string]bool{}
	var disks []string
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[1] != "disk" {
			continue
		}
		name := f[0]
		base := strings.TrimPrefix(name, "/dev/")
		if strings.HasPrefix(base, "loop") || strings.HasPrefix(base, "sr") ||
			strings.HasPrefix(base, "ram") || strings.HasPrefix(base, "nbd") {
			continue
		}
		if !seen[name] {
			seen[name] = true
			disks = append(disks, name)
		}
	}
	slices.Sort(disks)
	return disks, nil
}

// resolveDisk 返回目标磁盘：优先使用 --disk，否则根据根文件系统推断。
func resolveDisk(o *Options, log *logx.Logger) (string, error) {
	if o.Disk != "" {
		fi, err := os.Stat(o.Disk)
		if err != nil {
			return "", fmt.Errorf("磁盘 %s 不存在", o.Disk)
		}
		if fi.Mode()&os.ModeDevice == 0 || fi.Mode()&os.ModeCharDevice != 0 {
			return "", fmt.Errorf("%s 不是块设备", o.Disk)
		}
		return o.Disk, nil
	}
	src, err := rootDevice()
	if err != nil {
		return "", err
	}
	disks, err := rootDisks(src)
	if err != nil {
		return "", err
	}
	switch len(disks) {
	case 0:
		return "", fmt.Errorf("无法确定根文件系统 %s 所在的磁盘；请用 --disk /dev/sdX 指定", src)
	case 1:
		if log != nil {
			log.Info("检测到根文件系统所在磁盘 %s(来自 %s)", disks[0], src)
		}
		return disks[0], nil
	default:
		return "", fmt.Errorf("根文件系统跨越多块磁盘：%s；请用 --disk /dev/sdX 指定目标磁盘",
			strings.Join(disks, ", "))
	}
}

// diskID 返回磁盘的唯一标识：GPT 的 PTUUID 或 MBR 的 disk signature。
// 安装器用它在设备名变化后重新找到同一块磁盘。
func diskID(disk string) string {
	if out, err := system.Output("lsblk", "-dn", "-o", "PTUUID", disk); err == nil {
		if id := normalizeDiskID(out); id != "" {
			return id
		}
	}
	if out, err := system.Output("blkid", "-s", "PTUUID", "-o", "value", disk); err == nil {
		if id := normalizeDiskID(out); id != "" {
			return id
		}
	}
	out, err := system.Output("fdisk", "-l", disk)
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(strings.ToLower(line), "disk identifier") {
			continue
		}
		if i := strings.LastIndexByte(line, ':'); i >= 0 {
			return normalizeDiskID(line[i+1:])
		}
	}
	return ""
}

// normalizeDiskID 统一 lsblk/blkid/fdisk 的标识格式。
func normalizeDiskID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "0x")
	s = strings.Trim(s, "{}")
	return s
}

// partUUIDOn 返回磁盘上第一个分区的 PARTUUID，用于在安装器里定位磁盘。
func partUUIDOn(disk string) string {
	out, err := system.Output("lsblk", "-p", "-rn", "-o", "NAME,TYPE,PARTUUID", disk)
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[1] == "part" && f[2] != "" && f[2] != "-" {
			return f[2]
		}
	}
	return ""
}

// resolveKeep 分析需要保留的数据分区。
func resolveKeep(o *Options, disk string, log *logx.Logger) ([]keepdata.Part, error) {
	rootDev, _ := rootDevice()
	keep, err := keepdata.Analyze(disk, rootDev, o.KeepParts)
	if err != nil {
		return nil, err
	}
	log.Warn("--keep-data 将保留 %d 个分区；其余分区会被删除", len(keep))
	for _, p := range keep {
		if p.Mountpoint != "" {
			log.Warn("%s 当前挂载在 %s，重装前需要卸载", p.Path, p.Mountpoint)
		}
	}
	return keep, nil
}

// warnKeepLayout 提示 keep-data 的空闲区语义和多磁盘风险。
func warnKeepLayout(disk string, log *logx.Logger) {
	log.Warn("--keep-data 会在最大的空闲区域创建新系统；如果保留分区夹在多个被删除分区之间，" +
		"空闲区会被分割。建议尽量把数据分区放在磁盘末尾。")
	name := strings.TrimPrefix(disk, "/dev/")
	out, err := system.Output("lsblk", "-dn", "-o", "NAME,TYPE")
	if err != nil {
		return
	}
	var others []string
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[1] != "disk" || f[0] == name {
			continue
		}
		if strings.HasPrefix(f[0], "loop") || strings.HasPrefix(f[0], "sr") || strings.HasPrefix(f[0], "ram") {
			continue
		}
		others = append(others, "/dev/"+f[0])
	}
	if len(others) > 0 {
		log.Warn("--keep-data 会在任意磁盘上选择最大空闲空间；当前还存在其他磁盘：%s",
			strings.Join(others, ", "))
	}
}

// detectEFI 判断是否使用 UEFI 启动。
func detectEFI() bool {
	_, err := os.Stat("/sys/firmware/efi")
	return err == nil
}

// diskLabel 返回分区的磁盘标签类型(gpt/msdos)，无法判断时按 gpt 处理。
func diskLabel(disk string) (string, error) {
	if !system.Exists("parted") {
		// 没有 parted 时无法检查分区表，交由安装器创建 GPT。
		return "gpt", nil
	}
	out, err := system.Run("parted", "-s", disk, "print")
	if err != nil {
		lower := strings.ToLower(out)
		if strings.Contains(lower, "unrecognised") || strings.Contains(lower, "unrecognized") {
			return "gpt", nil
		}
		return "", system.ErrCommand("parted", err, out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "Partition Table:") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				label := fields[len(fields)-1]
				if label == "gpt" || label == "msdos" {
					return label, nil
				}
			}
		}
	}
	return "gpt", nil
}

// selectMirror 选择镜像，返回选中的镜像、安装器目录和按优先级排列的
// 候选安装器目录(用于单个文件下载失败时切换镜像)。
func selectMirror(o *Options, suite, arch string, daily bool) (mirror.Mirror, string, []string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if daily {
		base := mirror.DailyNetbootDir(arch)
		m := mirror.Mirror{Protocol: "https", Host: "deb.debian.org", Directory: "/debian"}
		if o.China {
			// 每日构建固定从 d-i.debian.org 下载，这里只给目标系统选 apt 镜像。
			if ms := mirror.ByLatency(ctx, mirror.ChinaMirrors, suite); len(ms) > 0 {
				m = ms[0]
			}
		} else if user := userMirror(o); user != nil {
			m = *user
		}
		return m, base, []string{base}, nil
	}

	var cands []mirror.Mirror
	if o.China {
		// 国内镜像按实测延迟排序，官方镜像排在后面兜底。
		cands = append(mirror.ByLatency(ctx, mirror.ChinaMirrors, suite), mirror.Candidates(nil)...)
	} else {
		cands = mirror.Candidates(userMirror(o))
	}
	m, err := mirror.Select(ctx, cands, suite, arch)
	if err != nil {
		return mirror.Mirror{}, "", nil, err
	}
	base := m.NetbootDir(suite, arch)
	bases := []string{base}
	for _, c := range cands {
		b := c.NetbootDir(suite, arch)
		if b != base {
			bases = append(bases, b)
		}
	}
	return m, base, bases, nil
}

// userMirror 把国内预设或已校验的 --mirror 参数转换为 Mirror。
func userMirror(o *Options) *mirror.Mirror {
	if o.China {
		return &mirror.Mirror{Protocol: "https", Host: "mirrors.aliyun.com", Directory: "/debian"}
	}
	if o.Mirror == "" {
		return nil
	}
	u, err := url.Parse(o.Mirror)
	if err != nil || u.Host == "" {
		return nil
	}
	dir := u.Path
	if dir == "" {
		dir = "/debian"
	}
	return &mirror.Mirror{Protocol: strings.ToLower(u.Scheme), Host: u.Host, Directory: dir}
}

// checkBootSpace 检查 /boot 剩余空间，不足时返回警告(不阻断流程)。
func checkBootSpace() error {
	out, err := system.Output("df", "-Pk", "/boot")
	if err != nil {
		return nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return nil
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return nil
	}
	avail, _ := strconv.ParseInt(fields[3], 10, 64)
	if avail < 300*1024 {
		return fmt.Errorf("/boot 仅剩 %d MB 可用，建议至少保留 300 MB", avail/1024)
	}
	return nil
}

// logPath 非 root 时退回到当前目录的日志文件。
func logPath(p string) string {
	if system.IsRoot() {
		return p
	}
	return "clearme.log"
}
