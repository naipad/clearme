// Package payload 渲染要注入 initrd 的 preseed 和辅助脚本。
package payload

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"text/template"

	"github.com/naipad/clearme/internal/assets"
	"github.com/naipad/clearme/internal/cpio"
)

// FstabEntry 是安装后要挂载的保留分区。
type FstabEntry struct {
	PartUUID   string
	MountPoint string
	FSType     string
	Pass       int
}

// Config 是渲染安装器负载所需的全部配置。
type Config struct {
	Interface string
	Static    bool
	IP        string
	Netmask   string
	Gateway   string
	DNS       string
	Hostname  string

	// 目标系统的完整网络配置；为空表示不改写。
	Interfaces string
	ResolvConf string

	MirrorProtocol string
	MirrorHost     string
	MirrorDir      string
	Suite          string
	SecurityRepo   string
	SecuritySuite  string

	Timezone string
	NTP      string

	Disk       string
	DiskLabel  string // gpt 或 msdos
	EFI        bool
	ESPSize    int
	Filesystem string
	// BtrfsCompress 是写入根分区 fstab 的压缩选项值，例如 zstd:3。
	BtrfsCompress string
	KeepParts     []int
	Arch          string

	NonFreeFirmware bool
	Username        string
	PasswordHash    string
	Install         string
	Upgrade         string // safe-upgrade | full-upgrade | ""
	SSHPort         int
	AuthorizedKeys  string // 完整文件内容，可以为空
	KeepFstab       []FstabEntry

	Optimize bool
	Swap     bool
	SwapSize string // 例如 1G；Swap 为真时生效
	SwapMB   int
	Packages string

	PostInstall string   // 首次启动执行的脚本内容
	ExtraRepos  []string // 额外的 apt 源行
	Unattended  bool
	Fail2ban    bool
	Proxy       string // 下载和 apt 使用的代理

	// InstallerSSH 在安装器环境中启动 sshd，方便远程查看日志或接管安装。
	InstallerSSH bool
}

// Build 渲染并返回要追加到 initrd 的文件列表。
func Build(cfg Config) ([]cpio.Entry, error) {
	preseed, err := Preseed(cfg)
	if err != nil {
		return nil, err
	}
	firstboot, err := renderFirstboot(cfg)
	if err != nil {
		return nil, err
	}

	entries := []cpio.Entry{
		cpio.File("preseed.cfg", 0644, []byte(preseed)),
		cpio.File("clearme-assets/late.sh", 0755, []byte(renderLate(cfg))),
		cpio.File("clearme-assets/firstboot.sh", 0755, firstboot),
		cpio.File("clearme-assets/early.sh", 0755, []byte(assets.EarlySh)),
	}
	// 安全仓库在 base 阶段还不可见；把钩子放进 base-installer 的钩子目录，
	// 让内核选择使用最新的安全更新版本，而不是先装旧内核再升级。
	// sid/testing 没有独立安全仓库，此时不需要这个钩子。
	if cfg.SecurityRepo != "" {
		hook, err := render("post-base-installer", assets.PostBaseInstallerTmpl, cfg)
		if err != nil {
			return nil, err
		}
		entries = append(entries, cpio.File("clearme-assets/post-base-installer.sh", 0755, hook))
	}
	if cfg.Optimize {
		files, err := assets.OptimizeFS.ReadDir("optimize")
		if err != nil {
			return nil, fmt.Errorf("读取优化脚本：%w", err)
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			data, err := assets.OptimizeFS.ReadFile("optimize/" + f.Name())
			if err != nil {
				return nil, fmt.Errorf("读取优化脚本 %s：%w", f.Name(), err)
			}
			entries = append(entries, cpio.File("clearme-assets/optimize/"+f.Name(), 0755, data))
		}
	}
	if cfg.Upgrade != "" {
		entries = append(entries, cpio.File("clearme-assets/cleanup.sh", 0755, []byte(assets.CleanupSh)))
	}
	// 无论是否保留分区，早期命令都要按磁盘标识重新定位目标设备。
	entries = append(entries, cpio.File("clearme-assets/partman-early.sh", 0755, []byte(assets.PartmanEarly)))

	if cfg.InstallerSSH {
		script, err := renderInstallerSSH(cfg)
		if err != nil {
			return nil, err
		}
		entries = append(entries,
			cpio.File("clearme-assets/installer-ssh.sh", 0755, script),
			cpio.File("configs/clearme-installer-pass", 0600, []byte(cfg.PasswordHash+"\n")),
		)
		if cfg.AuthorizedKeys != "" {
			entries = append(entries, cpio.File("configs/clearme-installer-keys", 0600, []byte(cfg.AuthorizedKeys)))
		}
	}
	return entries, nil
}

// renderInstallerSSH 渲染安装器内的 sshd 启动脚本。
func renderInstallerSSH(cfg Config) ([]byte, error) {
	return render("installer-ssh", assets.InstallerSSHTmpl, struct {
		Username string
		SSHPort  int
	}{Username: cfg.Username, SSHPort: cfg.SSHPort})
}

// Preseed 只渲染 preseed 文件本身(Build 和其他调用方共用)。
func Preseed(cfg Config) (string, error) {
	out, err := render("preseed", assets.PreseedTmpl, cfg)
	return string(out), err
}

func render(name, tmpl string, data any) ([]byte, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("渲染 %s 失败：%w", name, err)
	}
	return buf.Bytes(), nil
}

func renderFirstboot(cfg Config) ([]byte, error) {
	post := ""
	if strings.TrimSpace(cfg.PostInstall) != "" {
		post = b64(cfg.PostInstall)
	}
	swapSize, swapMB := cfg.SwapSize, cfg.SwapMB
	if swapSize == "" {
		swapSize = "1G"
	}
	if swapMB <= 0 {
		swapMB = 1024
	}
	return render("firstboot", assets.FirstbootTmpl, struct {
		ExtraRepos     bool
		Packages       string
		Swap           bool
		SwapSize       string
		SwapMB         int
		PostInstallB64 string
	}{
		ExtraRepos:     len(cfg.ExtraRepos) > 0,
		Packages:       cfg.Packages,
		Swap:           cfg.Swap,
		SwapSize:       swapSize,
		SwapMB:         swapMB,
		PostInstallB64: post,
	})
}

// b64 对字符串做 base64 编码，避免把任意内容直接拼进 shell 脚本。
func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func renderLate(cfg Config) string {
	var b strings.Builder
	b.WriteString(`#!/bin/sh
set -eu
TARGET=/target
ASSETS=/clearme-assets

mkdir -p "$TARGET/root/.clearme"
cp "$ASSETS/firstboot.sh" "$TARGET/root/.clearme/firstboot.sh"
chmod 755 "$TARGET/root/.clearme/firstboot.sh"
# 优化脚本只在启用系统优化时才会打包进 initrd。
if [ -d "$ASSETS/optimize" ]; then
    mkdir -p "$TARGET/root/.clearme/optimize"
    cp "$ASSETS"/optimize/*.sh "$TARGET/root/.clearme/optimize/"
    chmod 755 "$TARGET/root/.clearme/optimize/"*.sh
fi
# 清理脚本只在启用 apt 升级时才会打包，用于首启后的收尾清理。
if [ -x "$ASSETS/cleanup.sh" ]; then
    cp "$ASSETS/cleanup.sh" "$TARGET/root/.clearme/cleanup.sh"
    chmod 755 "$TARGET/root/.clearme/cleanup.sh"
fi

# apt 默认会保护正在运行的内核；这里关闭保护，以便安装阶段的
# full-upgrade 更新内核，并在安装收尾和首启后清理旧内核。
mkdir -p "$TARGET/etc/apt/apt.conf.d"
cat > "$TARGET/etc/apt/apt.conf.d/99-disable-kernel-protection" <<'APTCONF'
APT::Protect-Kernels "false";
APTCONF
`)
	if cfg.Proxy != "" {
		fmt.Fprintf(&b, `
# 目标系统的 apt 使用同一个代理，安装阶段和后续更新都会生效。
cat > "$TARGET/etc/apt/apt.conf.d/99clearme-proxy" <<'APTPROXY'
Acquire::http::Proxy "%s";
Acquire::https::Proxy "%s";
APTPROXY
chmod 600 "$TARGET/etc/apt/apt.conf.d/99clearme-proxy"
`, cfg.Proxy, cfg.Proxy)
	}
	if len(cfg.ExtraRepos) > 0 {
		b.WriteString(`
mkdir -p "$TARGET/etc/apt/sources.list.d"
cat > "$TARGET/etc/apt/sources.list.d/clearme-extra.list" <<'EXTRA'
`)
		for _, repo := range cfg.ExtraRepos {
			b.WriteString(repo)
			b.WriteByte('\n')
		}
		b.WriteString("EXTRA\n")
	}
	if cfg.Unattended {
		b.WriteString(`
# 自动安全更新：unattended-upgrades 已由 pkgsel 装入，这里只写周期策略。
mkdir -p "$TARGET/etc/apt/apt.conf.d"
cat > "$TARGET/etc/apt/apt.conf.d/20auto-upgrades" <<'AUTOUPGRADE'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
AUTOUPGRADE
`)
	}
	if cfg.Fail2ban {
		fmt.Fprintf(&b, `
# fail2ban 已由 pkgsel 装入；jail 在首次启动时由服务直接加载。
mkdir -p "$TARGET/etc/fail2ban/jail.d"
cat > "$TARGET/etc/fail2ban/jail.d/clearme-sshd.local" <<'FAIL2BAN'
[sshd]
enabled = true
port = %d
FAIL2BAN
in-target systemctl enable fail2ban >/dev/null 2>&1 || true
`, cfg.SSHPort)
	}
	if cfg.NTP != "" {
		// d-i 默认不安装 systemd-timesyncd，缺包时 enable 会报 unit 不存在。
		// 先补装再写配置，避免 dpkg 处理已存在的 conffile 时弹提示。
		fmt.Fprintf(&b, `
if [ ! -e "$TARGET/usr/lib/systemd/system/systemd-timesyncd.service" ] &&
    [ ! -e "$TARGET/lib/systemd/system/systemd-timesyncd.service" ]; then
    if ! in-target env DEBIAN_FRONTEND=noninteractive \
        apt-get install -y systemd-timesyncd >/dev/null 2>&1; then
        echo "clearme: systemd-timesyncd 安装失败，NTP 配置可能不生效" >&2
    fi
fi
# 把安装器使用的 NTP 服务器写进目标系统的 systemd-timesyncd。
mkdir -p "$TARGET/etc/systemd"
cat > "$TARGET/etc/systemd/timesyncd.conf" <<'TIMESYNC'
[Time]
NTP=%s
TIMESYNC
in-target systemctl enable systemd-timesyncd >/dev/null 2>&1 || true
`, cfg.NTP)
	}
	b.WriteString(`
cat > "$TARGET/etc/systemd/system/clearme-firstboot.service" <<'UNIT'
[Unit]
Description=One-time first-boot initialization
After=network.target network-online.target
Wants=network-online.target
ConditionPathExists=/root/.clearme/firstboot.sh

[Service]
Type=oneshot
ExecStart=/root/.clearme/firstboot.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
UNIT
mkdir -p "$TARGET/etc/systemd/system/multi-user.target.wants"
ln -sf /etc/systemd/system/clearme-firstboot.service \
    "$TARGET/etc/systemd/system/multi-user.target.wants/clearme-firstboot.service"

SSHD="$TARGET/etc/ssh/sshd_config"
if [ -f "$SSHD" ]; then
`)
	fmt.Fprintf(&b, "    sed -i -E 's/^#?Port .*/Port %d/' \"$SSHD\" || true\n", cfg.SSHPort)
	if cfg.Username == "root" {
		b.WriteString("    sed -i -E 's/^#?PermitRootLogin .*/PermitRootLogin yes/' \"$SSHD\" || true\n")
	} else {
		b.WriteString("    sed -i -E 's/^#?PermitRootLogin .*/PermitRootLogin no/' \"$SSHD\" || true\n")
	}
	if cfg.AuthorizedKeys != "" {
		b.WriteString("    sed -i -E 's/^#?PasswordAuthentication .*/PasswordAuthentication no/' \"$SSHD\" || true\n")
	}
	b.WriteString("fi\n")

	if cfg.AuthorizedKeys != "" {
		home := "/root"
		if cfg.Username != "root" {
			home = "/home/" + cfg.Username
		}
		keys := b64(cfg.AuthorizedKeys)
		fmt.Fprintf(&b, `
mkdir -p "$TARGET%s/.ssh"
chmod 700 "$TARGET%s/.ssh"
in-target sh -c 'echo %s | base64 -d > %s/.ssh/authorized_keys'
in-target chmod 600 %s/.ssh/authorized_keys
`, home, home, keys, home, home)
		if cfg.Username != "root" {
			fmt.Fprintf(&b, "in-target chown -R %s:%s %s/.ssh || true\n",
				cfg.Username, cfg.Username, home)
		}
	}

	if cfg.Username != "root" {
		fmt.Fprintf(&b, `
printf '%%s\n' '%s ALL=(ALL:ALL) NOPASSWD:ALL' > "$TARGET/etc/sudoers.d/90-user-%s"
chmod 440 "$TARGET/etc/sudoers.d/90-user-%s"
`, cfg.Username, cfg.Username, cfg.Username)
	}

	if cfg.Interfaces != "" {
		// d-i 自己按安装时的网络写了一份 interfaces，这里整体替换为
		// 探测到的完整配置(IPv4 + IPv6 + 额外地址)。
		b.WriteString(`
mkdir -p "$TARGET/etc/network"
cat > "$TARGET/etc/network/interfaces" <<'IFACES'
`)
		b.WriteString(cfg.Interfaces)
		b.WriteString("IFACES\n")
	}
	if cfg.ResolvConf != "" {
		// systemd-resolved 的软链接保持原样，直接覆盖会破坏解析。
		b.WriteString(`
if [ ! -L "$TARGET/etc/resolv.conf" ]; then
cat > "$TARGET/etc/resolv.conf" <<'RESOLV'
`)
		b.WriteString(cfg.ResolvConf)
		b.WriteString("RESOLV\nfi\n")
	}

	if len(cfg.KeepFstab) > 0 {
		b.WriteString("\ncat >> \"$TARGET/etc/fstab\" <<'FSTAB'\n")
		for _, e := range cfg.KeepFstab {
			fmt.Fprintf(&b, "PARTUUID=%s %s %s defaults,nofail 0 %d\n", e.PartUUID, e.MountPoint, e.FSType, e.Pass)
		}
		b.WriteString("FSTAB\n")
		for _, e := range cfg.KeepFstab {
			fmt.Fprintf(&b, "mkdir -p \"$TARGET%s\"\n", e.MountPoint)
		}
	}

	if cfg.BtrfsCompress != "" {
		// d-i 的挂载选项列表只接受 compress 这个名称，不接受 compress=zstd:3，
		// 所以在安装收尾时直接给目标系统 fstab 的根分区行追加压缩选项。
		fmt.Fprintf(&b, `
if [ -f "$TARGET/etc/fstab" ]; then
    awk -v opt='compress=%s' '
        $2 == "/" {
            if (index("," $4 ",", ",compress=") == 0) {
                if ($4 == "") $4 = opt
                else $4 = $4 "," opt
            }
            print
            next
        }
        { print }
    ' "$TARGET/etc/fstab" > "$TARGET/etc/fstab.clearme"
    mv "$TARGET/etc/fstab.clearme" "$TARGET/etc/fstab"
fi
`, cfg.BtrfsCompress)
	}

	// 升级完成后清掉旧内核和孤儿依赖。失败只提示，不让整个安装中止；
	// 首次启动的清理脚本会再执行一次。
	b.WriteString(`
if ! in-target env DEBIAN_FRONTEND=noninteractive apt-get autoremove --purge -y; then
    echo "clearme: 安装阶段 autoremove 失败，首次启动后会再试一次" >&2
fi
`)

	b.WriteString("\nexit 0\n")
	return b.String()
}
