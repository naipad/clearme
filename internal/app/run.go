package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/naipad/clearme/internal/crypt"
	"github.com/naipad/clearme/internal/diudeb"
	"github.com/naipad/clearme/internal/grub"
	"github.com/naipad/clearme/internal/guard"
	"github.com/naipad/clearme/internal/initrd"
	"github.com/naipad/clearme/internal/keepdata"
	"github.com/naipad/clearme/internal/logx"
	"github.com/naipad/clearme/internal/mirror"
	"github.com/naipad/clearme/internal/payload"
	"github.com/naipad/clearme/internal/release"
	"github.com/naipad/clearme/internal/system"
	"github.com/naipad/clearme/internal/ui"
	"github.com/naipad/clearme/internal/version"
)

// Run 执行完整的重装准备流程。
func Run(o *Options) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("Windows 版本仅支持 --dry-run；请在需要重装的 Debian 系统上运行 clearme")
	}
	log, err := logx.New(logPath(o.LogFile))
	if err != nil {
		return fmt.Errorf("无法打开日志文件：%w", err)
	}
	defer log.Close()
	log.Info("clearme %s 启动", version.Version)

	if err := mirror.SetProxy(o.Proxy); err != nil {
		return err
	}
	if o.Proxy != "" {
		log.Info("下载和 apt 将使用代理")
	}

	res, err := guard.Check(o.Force)
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		log.Warn("%s", w)
	}

	info, err := resolveSuite(o, log)
	if err != nil {
		return err
	}
	suite, daily := info.Suite, info.Daily
	arch, err := targetArch()
	if err != nil {
		return err
	}
	log.Info("目标：Debian %s (%s)，架构 %s", suite, release.Display(suite), arch)

	// 生成密码：未指定 --password 时随机生成并在摘要中展示。
	password := o.Password
	random := false
	if password == "" {
		password, err = crypt.RandomPassword(16)
		if err != nil {
			return err
		}
		random = true
	}
	salt, err := crypt.RandomSalt()
	if err != nil {
		return err
	}
	passwordHash := crypt.SHA512Crypt(password, salt)

	// 网络。
	netcfg, netOpts, err := resolveNetwork(o, info.Number, log)
	if err != nil {
		return err
	}
	interfaces, resolvConf := renderNetwork(netcfg, netOpts)

	// 磁盘与引导方式。
	disk, err := resolveDisk(o, log)
	if err != nil {
		return err
	}
	efi := detectEFI()
	label, err := diskLabel(disk)
	if err != nil {
		return err
	}
	if efi && label != "gpt" {
		if o.KeepData {
			return fmt.Errorf("UEFI 启动时 --keep-data 要求 GPT 分区表，但 %s 当前为 %s", disk, label)
		}
		log.Info("为 UEFI 把磁盘标签从 %s 转换为 gpt", label)
		label = "gpt"
	}

	// 安装器里按磁盘标识重新定位，避免重启后设备名变化导致装错盘。
	diskIdent := diskID(disk)
	rootPart := partUUIDOn(disk)
	if diskIdent == "" && rootPart == "" {
		log.Warn("无法读取 %s 的磁盘标识，安装器将回退到设备名", disk)
	} else {
		log.Info("磁盘标识：%s，分区定位：%s", diskIdent, rootPart)
	}

	// 需要保留的数据分区。
	var keepParts []int
	var fstab []payload.FstabEntry
	mounts := make(map[int]string)
	if o.KeepData {
		keep, err := resolveKeep(o, disk, log)
		if err != nil {
			return err
		}
		// 只有能挂载的分区才占用 /data 序号，避免 fstab 与分区号错位。
		dataIdx := 0
		for _, p := range keep {
			if label == "msdos" && p.Num > 4 {
				return fmt.Errorf("无法在 msdos 磁盘上保留逻辑分区 %s；请改用主分区或 GPT 磁盘", p.Path)
			}
			keepParts = append(keepParts, p.Num)
			where := "不挂载"
			if keepdata.IsMountable(p.FSType) {
				if p.PartUUID == "" {
					return fmt.Errorf("分区 %s 没有 PARTUUID，无法保留", p.Path)
				}
				mount := keepdata.MountPointFor(dataIdx)
				dataIdx++
				mounts[p.Num] = mount
				where = mount
				fstab = append(fstab, payload.FstabEntry{
					PartUUID:   p.PartUUID,
					MountPoint: mount,
					FSType:     p.FSType,
					Pass:       keepdata.FstabPass(p.FSType),
				})
			}
			log.Info("保留 %s(%s %s)-> %s", p.Path, p.FSType, p.Size, where)
		}
		warnKeepLayout(disk, log)
	}

	// 镜像。
	m, base, bases, err := selectMirror(o, suite, arch, daily)
	if err != nil {
		return err
	}
	log.Info("使用镜像 %s", base)

	// SSH 公钥。
	keys := ""
	if o.AuthorizedKeysURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		keys, err = mirror.FetchText(ctx, o.AuthorizedKeysURL, 1<<20)
		cancel()
		if err != nil {
			return fmt.Errorf("无法下载 SSH 公钥：%w", err)
		}
		log.Info("已载入 SSH 公钥(%d 字节)", len(keys))
	}

	// 安装后钩子：URL 直接抓取，本地路径读取；两者都在准备阶段落地。
	postInstall := ""
	if o.PostInstall != "" {
		dctx, dcancel := context.WithTimeout(context.Background(), 60*time.Second)
		if isHTTPURL(o.PostInstall) {
			postInstall, err = mirror.FetchText(dctx, o.PostInstall, 1<<20)
		} else {
			err = readLocalScript(o.PostInstall, &postInstall)
		}
		dcancel()
		if err != nil {
			return fmt.Errorf("无法载入 --post-install 脚本：%w", err)
		}
		log.Info("已载入安装后脚本(%d 字节)", len(postInstall))
	}

	// 安装目录与下载。
	const dir = "/boot/clearme"
	if err := checkBootSpace(); err != nil {
		log.Warn("%v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	linuxPath := filepath.Join(dir, "linux")
	initrdPath := filepath.Join(dir, "initrd.gz")
	fwPath := filepath.Join(dir, "firmware.cpio.gz")
	log.Step("正在下载安装器内核和 initrd ...")
	dl := newDownloader(log)
	if err := dl.file(ctx, bases, "linux", linuxPath); err != nil {
		return fmt.Errorf("下载内核失败：%w", err)
	}
	if err := dl.file(ctx, bases, "initrd.gz", initrdPath); err != nil {
		return fmt.Errorf("下载 initrd 失败：%w", err)
	}
	if o.Firmware {
		fwURL := fmt.Sprintf("https://cdimage.debian.org/cdimage/unofficial/non-free/firmware/%s/current/firmware.cpio.gz", suite)
		if err := mirror.Download(ctx, fwURL, fwPath); err != nil {
			return fmt.Errorf("下载固件失败：%w", err)
		}
	}

	hostname := resolveHostname(o)

	// swap 决策：显式指定优先；小内存机器默认开启。
	swapOn := o.Swap
	if !o.Swap && guard.MemoryMB() > 0 && guard.MemoryMB() < 1024 {
		swapOn = true
	}
	swapSize, swapMB := "1G", 1024
	if o.SwapSize != "" {
		swapSize, swapMB = o.SwapSize, o.SwapMB
	}

	securityRepo := release.SecurityRepo(m.Protocol, m.Host, m.Directory, suite, daily)
	cfg := payload.Config{
		Interface:       netcfg.Interface,
		Static:          netcfg.Static,
		IP:              netcfg.IP,
		Netmask:         netcfg.Netmask,
		Gateway:         netcfg.Gateway,
		DNS:             strings.Join(netcfg.DNS(), " "),
		Hostname:        hostname,
		Interfaces:      interfaces,
		ResolvConf:      resolvConf,
		MirrorProtocol:  m.Protocol,
		MirrorHost:      m.Host,
		MirrorDir:       m.Directory,
		Suite:           suite,
		SecurityRepo:    securityRepo,
		SecuritySuite:   release.SecuritySuite(suite),
		Timezone:        o.Timezone,
		NTP:             o.NTP,
		Disk:            disk,
		DiskLabel:       label,
		EFI:             efi,
		ESPSize:         espSizeMB,
		Filesystem:      o.Filesystem,
		BtrfsCompress:   o.BtrfsCompress,
		KeepParts:       keepParts,
		Arch:            arch,
		NonFreeFirmware: true,
		Username:        o.Username,
		PasswordHash:    passwordHash,
		Install:         buildInstallList(o),
		Upgrade:         o.Upgrade,
		SSHPort:         o.SSHPort,
		AuthorizedKeys:  keys,
		KeepFstab:       fstab,
		Optimize:        o.Optimize,
		Swap:            swapOn,
		SwapSize:        swapSize,
		SwapMB:          swapMB,
		Packages:        o.Install,
		PostInstall:     postInstall,
		ExtraRepos:      o.AptRepos,
		Unattended:      o.Unattended,
		Fail2ban:        o.Fail2ban,
		Proxy:           o.Proxy,
		InstallerSSH:    o.InstallerSSH,
	}
	if cfg.Packages == "" {
		cfg.Packages = "ca-certificates"
	}

	entries, err := payload.Build(cfg)
	if err != nil {
		return err
	}
	if o.InstallerSSH {
		log.Step("正在准备安装器 SSH ...")
		udebEntries, err := diudeb.Inject(ctx, m, suite, arch, initrdPath, log.Step)
		if err != nil {
			return fmt.Errorf("准备安装器 SSH 失败：%w", err)
		}
		entries = append(udebEntries, entries...)
	}
	log.Step("正在把 preseed 和辅助脚本写入 initrd ...")
	if err := initrd.AppendCpio(initrdPath, initrdPath, entries); err != nil {
		return fmt.Errorf("重新打包 initrd 失败：%w", err)
	}

	// 生成 GRUB 启动项(路径相对于 /boot 所在分区)。
	relLinux, err := grub.RelPath(linuxPath)
	if err != nil {
		return err
	}
	relInitrd, err := grub.RelPath(initrdPath)
	if err != nil {
		return err
	}
	relFw := ""
	if o.Firmware {
		relFw, err = grub.RelPath(fwPath)
		if err != nil {
			return err
		}
	}
	kargs := buildKernelArgs(disk, keepParts, diskIdent, rootPart)
	entry := grub.Entry{KernelPath: relLinux, InitrdPath: relInitrd, Firmware: relFw, KernelArgs: kargs}

	printSummary(o, log, suite, arch, disk, netcfg, password, random, keepParts, mounts,
		m.Base(), securityRepo)

	if !o.Yes && !confirmProceed() {
		log.Info("已取消：未写入启动项，已下载的文件保留在 %s", dir)
		return nil
	}
	if err := grub.Install(entry); err != nil {
		return fmt.Errorf("写入启动项失败：%w", err)
	}
	if err := grub.SetOneTimeBoot(); err != nil {
		log.Warn("%v", err)
	}
	if o.InstallerSSH {
		log.Info("重启后可 SSH 登录安装器查看进度：tail -fn+1 /var/log/syslog（用户 %s，端口 %d）",
			o.Username, o.SSHPort)
	}
	if o.Reboot {
		log.Warn("安装准备完成，系统即将重启并进入 Debian 安装器。")
		system.Run("sync")
		return system.RunNoExit("reboot")
	}
	log.Warn("重启以开始重新安装。如需放弃本次重装，请在重启前执行：%s", resetCommand())
	return nil
}

// resetCommand 返回可直接复制的取消命令。
// 优先使用二进制自身的绝对路径，避免提示里的命令依赖当前工作目录。
func resetCommand() string {
	if exe, err := os.Executable(); err == nil && filepath.IsAbs(exe) {
		return shellQuote(exe) + " reset"
	}
	return "./clearme-" + runtime.GOARCH + " reset"
}

// shellQuote 在路径包含特殊字符时加单引号，保证可以直接粘贴执行。
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t'\"\\$`&;|<>()*?[]#~=") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// confirmProceed 在写 GRUB 前请求用户确认，优先从 /dev/tty 读取。
func confirmProceed() bool {
	var in io.Reader = os.Stdin
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer f.Close()
		in = f
	}
	fmt.Fprintf(ui.Stderr(), "\n%s\n%s",
		ui.Yellow("即将写入安装器启动项：下次启动会重新分区，并清空当前系统。"),
		ui.Bold("确认继续？[y/N]: "))
	var answer string
	fmt.Fscanln(in, &answer)
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// buildKernelArgs 生成安装器内核参数。
func buildKernelArgs(disk string, keep []int, diskID, rootPartUUID string) string {
	args := []string{"auto=true", "priority=critical", "lowmem/low=1", "preseed/file=/preseed.cfg"}
	args = append(args, "clearme_disk="+disk)
	if diskID != "" {
		args = append(args, "clearme_disk_id="+diskID)
	}
	if rootPartUUID != "" {
		args = append(args, "clearme_root_partuuid="+rootPartUUID)
	}
	if len(keep) > 0 {
		nums := make([]string, len(keep))
		for i, n := range keep {
			nums[i] = strconv.Itoa(n)
		}
		args = append(args, "clearme_keep="+strings.Join(nums, ","))
	}
	return strings.Join(args, " ")
}

// downloader 在多个候选镜像之间下载安装器文件。
// 每个镜像的 SHA256SUMS 只读取一次；已存在且校验通过的文件直接复用。
type downloader struct {
	log  *logx.Logger
	sums map[string]checksumSet
}

type checksumSet struct {
	sums map[string]string
	err  error
}

func newDownloader(log *logx.Logger) *downloader {
	return &downloader{log: log, sums: make(map[string]checksumSet)}
}

// checksums 返回 base 对应的校验和，并在本次运行中缓存结果。
func (d *downloader) checksums(ctx context.Context, base string) checksumSet {
	if cs, ok := d.sums[base]; ok {
		return cs
	}
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	sums, err := mirror.FetchChecksums(sctx, base)
	cancel()
	cs := checksumSet{sums: sums, err: err}
	d.sums[base] = cs
	return cs
}

// file 依次尝试候选镜像目录下载同一个文件，并用该目录的
// SHA256SUMS 校验；校验失败视为该镜像不可用，继续尝试下一个。
func (d *downloader) file(ctx context.Context, bases []string, name, dest string) error {
	var lastErr error
	for _, base := range bases {
		cs := d.checksums(ctx, base)
		if cs.err != nil {
			d.log.Info("无法读取 %s 的 SHA256SUMS，跳过校验", base)
		} else if d.skipVerified(dest, name, cs.sums) {
			return nil
		}
		if err := mirror.Download(ctx, base+"/"+name, dest); err != nil {
			lastErr = err
			d.log.Warn("从 %s 下载 %s 失败：%v", base, name, err)
			continue
		}
		if sum, ok := cs.sums[name]; ok {
			if err := mirror.VerifyFile(dest, sum); err != nil {
				lastErr = err
				d.log.Warn("镜像 %s 的文件校验失败：%v", base, err)
				os.Remove(dest)
				os.Remove(dest + ".part")
				continue
			}
			d.log.Success("%s 的 SHA-256 校验通过", name)
		} else if cs.err == nil {
			d.log.Warn("镜像 %s 未提供 %s 的校验和", base, name)
		}
		return nil
	}
	return lastErr
}

// skipVerified 报告 dest 已存在且与期望校验和一致。
func (d *downloader) skipVerified(dest, name string, sums map[string]string) bool {
	sum, ok := sums[name]
	if !ok {
		return false
	}
	st, err := os.Stat(dest)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return false
	}
	if err := mirror.VerifyFile(dest, sum); err != nil {
		return false
	}
	d.log.Success("%s 已存在且 SHA-256 校验通过，跳过下载", name)
	return true
}

// readLocalScript 在读取前检查文件类型和大小，避免把超大文件或设备读入内存。
func readLocalScript(path string, out *string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s 不是普通文件", path)
	}
	if fi.Size() > 1<<20 {
		return fmt.Errorf("脚本大于 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	*out = string(data)
	return nil
}

// isHTTPURL 判断 --post-install 是 URL 还是本地路径。
func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// buildInstallList 构建安装阶段由 pkgsel 装入目标系统的软件包列表：
// 用户指定的包、普通用户所需的 sudo，以及自动更新和 fail2ban。
// 这些包在安装阶段一次装好。
func buildInstallList(o *Options) string {
	pkgs := strings.Fields(o.Install)
	add := func(name string) {
		if !slices.Contains(pkgs, name) {
			pkgs = append(pkgs, name)
		}
	}
	// tasksel 只选 ssh-server 时，ifupdown 不会随 standard 任务装入，
	// 目标系统里就没有任何东西去 ifup 网卡。这里显式保证它存在。
	add("ifupdown")
	if o.Username != "root" {
		add("sudo")
	}
	if o.Unattended {
		add("unattended-upgrades")
	}
	if o.Fail2ban {
		add("fail2ban")
	}
	return strings.Join(pkgs, " ")
}
