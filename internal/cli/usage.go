package cli

import (
	"fmt"
	"io"

	"github.com/naipad/clearme/internal/ui"
	"github.com/naipad/clearme/internal/version"
)

// option 打印一条对齐的参数说明。
func option(w io.Writer, flags, desc string) {
	fmt.Fprintf(w, "  %s %s\n", ui.Cyan(fmt.Sprintf("%-32s", flags)), desc)
}

// section 打印一个帮助分组标题。
func section(w io.Writer, title string) {
	fmt.Fprintf(w, "\n%s\n", ui.Title(title))
}

// Usage 打印中文帮助。
func Usage() {
	w := ui.Stderr()
	fmt.Fprintf(w, "\n%s %s  %s\n", ui.Title("clearme"), ui.Dim(version.Version), ui.Bold("Debian 在线重装工具"))

	section(w, "用法")
	option(w, "clearme [选项]", "准备重装，重启后自动进入安装器")
	option(w, "clearme status", "查看安装文件、GRUB 启动项和日志状态")
	option(w, "clearme logs [类型]", "查看日志：clearme、firstboot、cleanup 或 all")
	option(w, "clearme reset", "删除安装器启动项和已下载的文件")
	option(w, "clearme -V", "显示程序版本(--version 仍表示 Debian 版本)")

	section(w, "目标与镜像")
	option(w, "--version VERSION", "目标版本：11/12/13/14/sid 或代号(默认跟随 stable)")
	option(w, "--mirror URL", "完整镜像地址(默认 deb.debian.org)")
	option(w, "--china", "使用国内镜像并自动测速")
	option(w, "--proxy URL", "下载与目标系统 apt 使用的代理")

	section(w, "网络")
	option(w, "--ip ADDR", "静态 IPv4 地址(默认沿用当前 DHCP/静态方式)")
	option(w, "--netmask MASK", "子网掩码，例如 255.255.255.0")
	option(w, "--gateway ADDR", "默认网关")
	option(w, "--ipv6 ADDR[/PREFIX]", "静态 IPv6 地址(网关默认沿用当前 RA)")
	option(w, "--ipv6-gateway ADDR", "IPv6 默认网关(留空沿用当前 RA/DHCPv6)")
	option(w, "--ipv6-dns \"ADDR ...\"", "IPv6 DNS 服务器(空格分隔)")
	option(w, "--dns \"SERVERS\"", "DNS 服务器(默认沿用当前系统)")
	option(w, "--hostname NAME", "主机名(默认沿用当前主机名)")

	section(w, "账户与 SSH")
	option(w, "--user NAME", "登录用户(默认 root)")
	option(w, "--password PASS", "登录密码；省略时随机生成并显示")
	option(w, "--ssh-port N", "SSH 端口(默认 22，安装期间同样生效)")
	option(w, "--authorized-keys-url URL", "首次启动写入 SSH 公钥，并关闭密码登录")

	section(w, "磁盘与引导")
	option(w, "--disk /dev/sdX", "目标磁盘(默认根分区所在磁盘)")
	option(w, "--filesystem FS", "根文件系统：ext4/xfs/btrfs(默认 ext4)")
	option(w, "--btrfs-compress ALGO", "btrfs 压缩，如 zstd:3(需 --filesystem btrfs)")
	option(w, "--keep-data", "保留数据分区(自动识别)")
	option(w, "--keep-part /dev/sdX3", "指定要保留的分区，可重复")

	section(w, "系统与初始化")
	option(w, "--install \"PKG ...\"", "额外安装的软件包(空格分隔)")
	option(w, "--upgrade none|safe|full", "安装阶段 apt 升级(默认 full)")
	option(w, "--post-install PATH|URL", "首次启动执行的脚本(本地文件或 URL)")
	option(w, "--apt-repo 'deb ...'", "额外 apt 源，可重复")
	option(w, "--fail2ban", "安装 fail2ban 并启用 SSH 防爆破")
	option(w, "--firmware", "附带 non-free 固件")
	option(w, "--swap-size SIZE", "创建 swapfile，如 512M、2G、2048MB(默认按内存判断)")

	section(w, "运行方式")
	option(w, "--dry-run", "打印配置摘要，不修改系统")
	option(w, "--yes", "跳过确认")
	option(w, "--reboot-now", "准备完成后立即重启")
	option(w, "--force", "跳过容器/Secure Boot 等环境检查")

	section(w, "默认值")
	fmt.Fprintf(w, "  %s\n", ui.Dim("跟随 stable · ext4 · 自动 UEFI/BIOS · Asia/Shanghai · 自动安全更新 · 安装器 SSH 开启 · 小内存自动 swap"))

	section(w, "示例")
	fmt.Fprintf(w, "  %s\n", ui.Dim("clearme --version 13 --china --hostname web01"))
	fmt.Fprintf(w, "  %s\n", ui.Dim("clearme --keep-data --dry-run"))
	fmt.Fprintf(w, "  %s\n", ui.Dim("clearme --filesystem btrfs --btrfs-compress zstd:3"))
	fmt.Fprintf(w, "\n%s\n", ui.Dim("完整文档：https://github.com/naipad/clearme"))
}
