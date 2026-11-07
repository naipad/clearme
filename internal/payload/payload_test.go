package payload

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naipad/clearme/internal/assets"
	"github.com/naipad/clearme/internal/cpio"
)

func baseConfig() Config {
	return Config{
		Interface: "eth0", Static: true, IP: "192.0.2.10", Netmask: "255.255.255.0",
		Gateway: "192.0.2.1", DNS: "1.1.1.1", Hostname: "test",
		MirrorProtocol: "https", MirrorHost: "deb.debian.org", MirrorDir: "/debian",
		Suite: "trixie", SecurityRepo: "https://deb.debian.org/debian-security",
		SecuritySuite: "trixie-security", Timezone: "UTC", NTP: "time.cloudflare.com",
		Disk: "/dev/sda", DiskLabel: "gpt", EFI: true, ESPSize: 106,
		Filesystem: "ext4", Arch: "amd64", NonFreeFirmware: true,
		Username: "root", PasswordHash: "$6$salt$hash", Install: "curl",
		SSHPort: 22, Optimize: true, Packages: "curl",
	}
}

func TestPreseedEFI(t *testing.T) {
	out, err := Preseed(baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"d-i netcfg/get_ipaddress string 192.0.2.10",
		"d-i partman-auto/disk string /dev/sda",
		"method{ efi }",
		"d-i grub-installer/bootdev string /dev/sda",
		"d-i passwd/root-login boolean true",
		"d-i preseed/late_command string sh /clearme-assets/late.sh",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preseed missing %q\n%s", want, out)
		}
	}
}

// TestPartmanEarlyAlwaysIncluded 检查普通模式也会带上磁盘定位脚本。
func TestPartmanEarlyAlwaysIncluded(t *testing.T) {
	cfg := baseConfig()
	entries, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, e := range entries {
		if e.Name == "clearme-assets/partman-early.sh" {
			script = string(e.Data)
		}
	}
	if script == "" {
		t.Fatal("普通模式也应打包 partman-early.sh")
	}
	for _, want := range []string{
		"clearme_disk_id",
		"clearme_root_partuuid",
		"/dev/disk/by-partuuid/",
		"debconf-set partman-auto/disk",
		"debconf-set grub-installer/bootdev",
		"rm -rf /target/boot/efi/*",
		"bootstrap-base.postinst",
		"oldIFS=$IFS",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("partman-early.sh missing %q", want)
		}
	}
	preseed, err := Preseed(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preseed, "d-i partman/early_command string sh /clearme-assets/partman-early.sh") {
		t.Error("preseed 应在所有模式调用 partman-early.sh")
	}
}

// TestBtrfsCompress 检查压缩选项只在启用时写入 late.sh 的 fstab 补丁。
func TestBtrfsCompress(t *testing.T) {
	lateScript := func(cfg Config) string {
		t.Helper()
		entries, err := Build(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name == "clearme-assets/late.sh" {
				return string(e.Data)
			}
		}
		t.Fatal("缺少 late.sh")
		return ""
	}

	cfg := baseConfig()
	cfg.Filesystem = "btrfs"
	cfg.BtrfsCompress = "zstd:3"
	late := lateScript(cfg)
	for _, want := range []string{
		"-v opt='compress=zstd:3'",
		`$2 == "/"`,
		`index("," $4 ",", ",compress=")`,
		`"$TARGET/etc/fstab"`,
	} {
		if !strings.Contains(late, want) {
			t.Errorf("late.sh missing %q", want)
		}
	}

	if late := lateScript(baseConfig()); strings.Contains(late, "compress=") {
		t.Error("未启用 --btrfs-compress 时不应写压缩选项")
	}
}

// TestInstallerSSH 检查启用安装器 SSH 时会注入启动脚本、密码和公钥，
// 未启用时不应出现在 initrd 或 preseed 里。
func TestInstallerSSH(t *testing.T) {
	enabled := baseConfig()
	enabled.InstallerSSH = true
	enabled.Username = "deploy"
	enabled.SSHPort = 2222
	enabled.AuthorizedKeys = "ssh-ed25519 AAAA test\n"

	entries, err := Build(enabled)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]cpio.Entry{}
	for _, e := range entries {
		found[e.Name] = e
	}
	script, ok := found["clearme-assets/installer-ssh.sh"]
	if !ok {
		t.Fatal("缺少 installer-ssh.sh")
	}
	for _, want := range []string{
		"PORT=2222", "USER_NAME=deploy", "/usr/sbin/sshd", "ssh-keygen -A",
		"PrintMotd yes", "/etc/motd", "tail -fn+1 /var/log/syslog",
		"dmesg -w", "screen -X multiuser on", "screen -x root/$STY",
		"/run/clearme-sshd.lock",
	} {
		if !strings.Contains(string(script.Data), want) {
			t.Errorf("installer-ssh.sh missing %q", want)
		}
	}
	// 密码文件始终注入，安装器与目标系统用同一套凭据。
	if pass := found["configs/clearme-installer-pass"]; pass.Mode&0777 != 0600 ||
		!strings.Contains(string(pass.Data), "$6$salt$hash") {
		t.Errorf("安装器密码文件不正确：%+v", pass)
	}
	if keys, ok := found["configs/clearme-installer-keys"]; !ok || !strings.Contains(string(keys.Data), "ssh-ed25519") {
		t.Errorf("安装器公钥文件不正确：%+v", keys)
	}
	// 非 root 账户时不得开放 root 密码登录。
	if strings.Contains(string(script.Data), "PermitRootLogin yes") {
		t.Error("USER_NAME 非 root 时不应写 PermitRootLogin yes")
	}
	if !strings.Contains(string(script.Data), "PermitRootLogin $root_login") {
		t.Error("installer-ssh.sh 应按账户决定 PermitRootLogin")
	}

	// 无公钥时退回密码登录，密码文件必须存在且权限 0600。
	noKeys := baseConfig()
	noKeys.InstallerSSH = true
	noKeys.Username = "root"
	entries, err = Build(noKeys)
	if err != nil {
		t.Fatal(err)
	}
	found = map[string]cpio.Entry{}
	for _, e := range entries {
		found[e.Name] = e
	}
	if pass := found["configs/clearme-installer-pass"]; pass.Mode&0777 != 0600 ||
		!strings.Contains(string(pass.Data), "$6$salt$hash") {
		t.Errorf("无公钥时安装器密码文件不正确：%+v", pass)
	}
	if _, ok := found["configs/clearme-installer-keys"]; ok {
		t.Error("无公钥时不应注入公钥文件")
	}
	rootScript, err := renderInstallerSSH(noKeys)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rootScript), "ensure_account root 0 /") ||
		!strings.Contains(string(rootScript), "root_login=yes") {
		t.Error("USER_NAME 为 root 时应创建 root 并允许 root 登录")
	}
	preseed, err := Preseed(enabled)
	if err != nil {
		t.Fatal(err)
	}
	// 早期命令统一走 early.sh，由它按需启动安装器 SSH。
	if !strings.Contains(preseed, "d-i preseed/early_command string sh /clearme-assets/early.sh") {
		t.Error("preseed 应调用 early.sh")
	}

	disabled := baseConfig()
	entries, err = Build(disabled)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name, "configs/") || e.Name == "clearme-assets/installer-ssh.sh" {
			t.Errorf("未启用时不应注入 %s", e.Name)
		}
	}
	preseed, err = Preseed(disabled)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preseed, "d-i preseed/early_command string sh /clearme-assets/early.sh") {
		t.Error("early.sh 与安装器 SSH 无关，任何模式都应写入")
	}
	if _, ok := found["clearme-assets/early.sh"]; !ok {
		t.Error("缺少 early.sh")
	}
}

// TestPostBaseInstallerHook 检查安全仓库钩子会注入到 initrd，
// 且 early.sh 会把它放进 base-installer 的钩子目录。
func TestPostBaseInstallerHook(t *testing.T) {
	cfg := baseConfig()
	entries, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]cpio.Entry{}
	for _, e := range entries {
		found[e.Name] = e
	}

	hook, ok := found["clearme-assets/post-base-installer.sh"]
	if !ok {
		t.Fatal("缺少 post-base-installer.sh")
	}
	for _, want := range []string{
		"https://deb.debian.org/debian-security", "trixie-security main",
		"/target/etc/apt/sources.list", "chroot /target apt-get update",
		// d-i 改用 deb822 格式时的兜底。
		"sources.list.d", "Types: deb",
	} {
		if !strings.Contains(string(hook.Data), want) {
			t.Errorf("post-base-installer.sh missing %q", want)
		}
	}

	early, ok := found["clearme-assets/early.sh"]
	if !ok {
		t.Fatal("缺少 early.sh")
	}
	for _, want := range []string{
		"/clearme-assets/installer-ssh.sh",
		"/usr/lib/post-base-installer.d",
		"/clearme-assets/post-base-installer.sh",
	} {
		if !strings.Contains(string(early.Data), want) {
			t.Errorf("early.sh missing %q", want)
		}
	}

	// sid/testing 没有独立安全仓库，不应写入钩子。
	cfg.SecurityRepo = ""
	entries, err = Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "clearme-assets/post-base-installer.sh" {
			t.Error("没有安全仓库时不应注入钩子")
		}
	}
}

// TestRenderedShellSyntax 用 sh -n 检查渲染后的脚本语法；没有 sh 时跳过。
func TestRenderedShellSyntax(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh 不可用，跳过脚本语法检查")
	}
	cfg := baseConfig()
	cfg.KeepParts = []int{2}
	cfg.KeepFstab = []FstabEntry{{PartUUID: "abcd-02", MountPoint: "/data", FSType: "ext4", Pass: 2}}
	cfg.Upgrade = "full-upgrade"
	cfg.Unattended = true
	cfg.Fail2ban = true
	cfg.ExtraRepos = []string{"deb https://example.com/debian trixie main"}
	cfg.InstallerSSH = true
	cfg.AuthorizedKeys = "ssh-ed25519 AAAA test\n"
	fb, err := renderFirstboot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sshScript, err := renderInstallerSSH(cfg)
	if err != nil {
		t.Fatal(err)
	}
	hook, err := render("post-base-installer", assets.PostBaseInstallerTmpl, cfg)
	if err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"late.sh":                renderLate(cfg),
		"firstboot.sh":           string(fb),
		"installer-ssh.sh":       string(sshScript),
		"early.sh":               assets.EarlySh,
		"post-base-installer.sh": string(hook),
		"cleanup.sh":             assets.CleanupSh,
	}
	for name, body := range scripts {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
			t.Errorf("%s 语法错误: %v\n%s", name, err, out)
		}
	}
}

func TestPreseedBIOSKeepData(t *testing.T) {
	cfg := baseConfig()
	cfg.EFI = false
	cfg.KeepParts = []int{2}
	cfg.DiskLabel = "gpt"
	cfg.KeepFstab = []FstabEntry{{PartUUID: "abcd-02", MountPoint: "/data", FSType: "ext4", Pass: 2}}
	out, err := Preseed(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"method{ biosgrub }",
		"d-i partman/early_command string sh /clearme-assets/partman-early.sh",
		"d-i partman-auto/init_automatically_partition select biggest_free",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preseed missing %q", want)
		}
	}
	if strings.Contains(out, "d-i partman-auto/method string regular") {
		t.Error("keep-data must not use the regular method: it recreates the disk label")
	}
	if strings.Contains(out, "$reusemethod{ }") {
		t.Error("keep-data must not reuse existing partitions")
	}
	entries, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "clearme-assets/partman-early.sh" {
			found = true
		}
	}
	if !found {
		t.Error("partman-early.sh not included")
	}
}

func TestLateScriptNetwork(t *testing.T) {
	cfg := baseConfig()
	cfg.Interfaces = "auto lo\niface lo inet loopback\n\niface ens3 inet6 static\n    address 2001:db8::10/64\n"
	cfg.ResolvConf = "nameserver 1.1.1.1\n"
	out := renderLate(cfg)
	for _, want := range []string{
		`mkdir -p "$TARGET/etc/network"`,
		"cat > \"$TARGET/etc/network/interfaces\" <<'IFACES'",
		"iface ens3 inet6 static",
		"    address 2001:db8::10/64",
		`if [ ! -L "$TARGET/etc/resolv.conf" ]; then`,
		"nameserver 1.1.1.1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("late.sh missing %q\n%s", want, out)
		}
	}
	// 两个 heredoc 的开头和结尾各出现一次。
	if got := strings.Count(out, "IFACES"); got != 2 {
		t.Errorf("IFACES heredoc markers = %d, want 2", got)
	}
	if got := strings.Count(out, "RESOLV"); got != 2 {
		t.Errorf("RESOLV heredoc markers = %d, want 2", got)
	}

	empty := baseConfig()
	if strings.Contains(renderLate(empty), "/etc/network/interfaces") {
		t.Error("没有网络配置时不应改写 interfaces")
	}
}

// TestFixEthName 检查网卡改名脚本与一次性单元都被打包，且 late.sh 把它们
// 装进目标系统并在 multi-user.target.wants 里启用。
func TestFixEthName(t *testing.T) {
	entries, err := Build(baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name] = true
	}
	for _, want := range []string{
		"clearme-assets/fix-eth-name.sh",
		"clearme-assets/fix-eth-name.service",
	} {
		if !found[want] {
			t.Errorf("initrd 缺少 %s", want)
		}
	}

	late := renderLate(baseConfig())
	for _, want := range []string{
		`cp "$ASSETS/fix-eth-name.sh" "$TARGET/root/.clearme/fix-eth-name.sh"`,
		`cp "$ASSETS/fix-eth-name.service" "$TARGET/etc/systemd/system/clearme-fix-eth-name.service"`,
		"multi-user.target.wants/clearme-fix-eth-name.service",
	} {
		if !strings.Contains(late, want) {
			t.Errorf("late.sh missing %q", want)
		}
	}

	// 改名必须早于 ifup，否则改完也来不及。
	if !strings.Contains(assets.FixEthNameService, "Before=networking.service") {
		t.Error("fix-eth-name.service 必须排在 networking.service 之前")
	}
	if !strings.Contains(assets.FixEthNameService, "ConditionPathExists=") {
		t.Error("fix-eth-name.service 需要 ConditionPathExists 保证一次性")
	}
	if !strings.Contains(assets.FixEthNameSh, "'# mac '*") {
		t.Error("fix-eth-name.sh 需要识别 # mac 标记")
	}
	if !strings.Contains(assets.FixEthNameSh, "/master") {
		t.Error("fix-eth-name.sh 需要跳过带 master 的从设备")
	}
}

func TestOptimizeScripts(t *testing.T) {
	entries, err := Build(baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutPrefix(e.Name, "clearme-assets/optimize/"); ok {
			scripts[name] = string(e.Data)
			names = append(names, name)
		}
	}
	wantNames := []string{"10-sysctl.sh", "20-limits.sh", "30-journald.sh", "40-logrotate.sh"}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("optimize scripts = %v, want %v", names, wantNames)
	}

	sysctl := scripts["10-sysctl.sh"]
	for _, want := range []string{
		"/etc/sysctl.d/99-sysctl.conf",
		"net.ipv4.tcp_congestion_control = bbr",
		"# ---- 内存与缓存 ----",
		"vm.swappiness                   = 10",
		"vm.vfs_cache_pressure           = 50",
		"vm.max_map_count                = 262144",
		"kernel.pid_max                  = 4194304",
		"fs.inotify.max_user_watches     = 1048576",
		"net.core.netdev_max_backlog     = 16384",
		"net.ipv4.tcp_slow_start_after_idle = 0",
		"net.ipv4.tcp_fastopen           = 3",
	} {
		if !strings.Contains(sysctl, want) {
			t.Errorf("10-sysctl.sh missing %q", want)
		}
	}
	// sysctl 只支持整行注释，值后面不能再出现 #（否则会被当成值的一部分）。
	for line := range strings.SplitSeq(sysctl, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") && strings.Contains(line, "#") {
			t.Errorf("inline comment after sysctl value: %q", line)
		}
	}

	for name, want := range map[string]string{
		"20-limits.sh":    "/etc/security/limits.d/90-nofile.conf",
		"30-journald.sh":  "/etc/systemd/journald.conf",
		"40-logrotate.sh": "/etc/logrotate.conf",
	} {
		if !strings.Contains(scripts[name], want) {
			t.Errorf("%s missing %q", name, want)
		}
	}

	// 关闭优化时不应打包。
	cfg := baseConfig()
	cfg.Optimize = false
	entries, err = Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name, "clearme-assets/optimize/") {
			t.Errorf("%s should not be included when optimization is disabled", e.Name)
		}
	}

	// late.sh 负责把脚本目录放进 /root/.clearme，firstboot 再逐个执行。
	if out := renderLate(baseConfig()); !strings.Contains(out, `cp "$ASSETS"/optimize/*.sh`) {
		t.Error("late.sh should copy the optimize scripts")
	}
	fb, err := renderFirstboot(baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fb), "/root/.clearme/optimize/*.sh") {
		t.Error("firstboot.sh should run the optimize scripts")
	}
	if strings.Contains(string(fb), "99-bbr.conf") {
		t.Error("firstboot.sh should no longer write BBR inline")
	}
}

func TestUpgradeCleanup(t *testing.T) {
	cfg := baseConfig()
	cfg.Upgrade = "full-upgrade"
	entries, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var cleanup string
	for _, e := range entries {
		if e.Name == "clearme-assets/cleanup.sh" {
			cleanup = string(e.Data)
		}
	}
	if cleanup == "" {
		t.Fatal("cleanup.sh not included when upgrade is enabled")
	}
	for _, want := range []string{
		"apt-get autoremove --purge -y",
		"apt-get clean",
		"rm -rf /root/.clearme",
		`rm -f "$LOG"`,
	} {
		if !strings.Contains(cleanup, want) {
			t.Errorf("cleanup.sh missing %q", want)
		}
	}

	late := renderLate(cfg)
	for _, want := range []string{
		"/etc/apt/apt.conf.d/99-disable-kernel-protection",
		`APT::Protect-Kernels "false";`,
		`cp "$ASSETS/cleanup.sh"`,
	} {
		if !strings.Contains(late, want) {
			t.Errorf("late.sh missing %q", want)
		}
	}

	for _, want := range []string{
		"in-target env DEBIAN_FRONTEND=noninteractive apt-get autoremove --purge -y",
	} {
		if !strings.Contains(late, want) {
			t.Errorf("late.sh missing %q", want)
		}
	}

	fb, err := renderFirstboot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"clearme-cleanup.service",
		"ExecStart=/root/.clearme/cleanup.sh",
		"uname -r",
		"sort -V",
		"sh /root/.clearme/cleanup.sh",
		"rm -f /var/log/clearme-firstboot.log",
		"rm -f /var/log/clearme-installer-ssh.log",
		"rm -rf /var/log/installer",
		"rm -f /etc/systemd/system/multi-user.target.wants/clearme-firstboot.service",
	} {
		if !strings.Contains(string(fb), want) {
			t.Errorf("firstboot.sh missing %q", want)
		}
	}
	// 首次启动只做本地初始化，不执行 dist-upgrade。
	if strings.Contains(string(fb), "dist-upgrade") {
		t.Error("firstboot.sh 不应再执行 dist-upgrade")
	}

	// 不升级时不打包 cleanup 脚本，但安装收尾仍会 autoremove。
	cfg.Upgrade = ""
	entries, err = Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "clearme-assets/cleanup.sh" {
			t.Error("cleanup.sh should not be included when --upgrade none")
		}
	}
	if late := renderLate(cfg); !strings.Contains(late, "apt-get autoremove --purge -y") {
		t.Error("--upgrade none 时安装收尾也应 autoremove")
	}
}

// TestExtendedInitOptions 覆盖代理、额外 apt 源、timesyncd、swap 大小、
// unattended-upgrades 和安装后脚本。
func TestExtendedInitOptions(t *testing.T) {
	cfg := baseConfig()
	cfg.Proxy = "http://127.0.0.1:3128"
	cfg.ExtraRepos = []string{"deb https://example.com/debian trixie main"}
	cfg.NTP = "ntp.example"
	cfg.Swap = true
	cfg.SwapSize = "2G"
	cfg.SwapMB = 2048
	cfg.Unattended = true
	cfg.Fail2ban = true
	cfg.SSHPort = 2222
	cfg.PostInstall = "#!/bin/sh\necho post-install-ran\n"

	preseed, err := Preseed(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preseed, "d-i mirror/http/proxy string http://127.0.0.1:3128") {
		t.Errorf("preseed missing proxy\n%s", preseed)
	}

	late := renderLate(cfg)
	for _, want := range []string{
		"/etc/apt/apt.conf.d/99clearme-proxy",
		`Acquire::http::Proxy "http://127.0.0.1:3128";`,
		`Acquire::https::Proxy "http://127.0.0.1:3128";`,
		`chmod 600 "$TARGET/etc/apt/apt.conf.d/99clearme-proxy"`,
		"/etc/apt/sources.list.d/clearme-extra.list",
		"deb https://example.com/debian trixie main",
		"/etc/systemd/timesyncd.conf",
		"NTP=ntp.example",
		"/usr/lib/systemd/system/systemd-timesyncd.service",
		"apt-get install -y systemd-timesyncd",
		"systemctl enable systemd-timesyncd",
		// 自动更新的包由 pkgsel 装入，配置在安装收尾写入。
		"/etc/apt/apt.conf.d/20auto-upgrades",
		`APT::Periodic::Unattended-Upgrade "1";`,
		`APT::Periodic::AutocleanInterval "7";`,
		// fail2ban 同样由 pkgsel 装入，jail 配置在安装收尾写入。
		"/etc/fail2ban/jail.d/clearme-sshd.local",
		"port = 2222",
		"systemctl enable fail2ban",
	} {
		if !strings.Contains(late, want) {
			t.Errorf("late.sh missing %q", want)
		}
	}

	fb, err := renderFirstboot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// 额外源在 pkgsel 之后才写入，这里保留一次补装。
		"apt-get update -y || true",
		"apt-get install -y --no-install-recommends curl",
		"fallocate -l 2G /swapfile",
		"count=2048",
		"chattr +C /swapfile",
		"btrfs property set /swapfile compression none",
		"swapon /swapfile",
		"/root/.clearme/post-install.sh",
	} {
		if !strings.Contains(string(fb), want) {
			t.Errorf("firstboot.sh missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"apt-get install -y --no-install-recommends unattended-upgrades",
		"apt-get install -y --no-install-recommends fail2ban",
		"20auto-upgrades",
		"/etc/fail2ban/jail.d",
	} {
		if strings.Contains(string(fb), unwanted) {
			t.Errorf("firstboot.sh 不应再处理 %q", unwanted)
		}
	}
	if strings.Contains(string(fb), "post-install-ran") {
		t.Error("安装后脚本应 base64 编码，不能直接内联")
	}

	// 没有额外源时，安装阶段已经装齐所有包，首次启动不应再跑 apt。
	off, err := renderFirstboot(baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(off), "apt-get") {
		t.Error("没有额外源时 firstboot.sh 不应再执行 apt")
	}
}

func TestLateScriptEscaping(t *testing.T) {
	cfg := baseConfig()
	cfg.Username = "alice"
	cfg.AuthorizedKeys = "ssh-ed25519 AAAA test\n"
	out := renderLate(cfg)
	for _, want := range []string{
		"Port 22",
		"PermitRootLogin no",
		"PasswordAuthentication no",
		"/home/alice/.ssh/authorized_keys",
		"90-user-alice",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("late.sh missing %q", want)
		}
	}
	if strings.Contains(out, "ssh-ed25519 AAAA") {
		t.Error("authorized keys should be base64 encoded, not inline")
	}
}
