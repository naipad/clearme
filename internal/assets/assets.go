// Package assets 内嵌安装器需要的所有文本资源。
package assets

import (
	"embed"
)

// PreseedTmpl 是 Debian 安装器的 preseed 模板。
//
//go:embed preseed/preseed.tmpl
var PreseedTmpl string

// FirstbootTmpl 是新系统首次启动时执行的一次性初始化脚本。
//
//go:embed firstboot/firstboot.sh.tmpl
var FirstbootTmpl string

// CleanupSh 是升级并重启后清理旧内核与残留文件的脚本。
//
//go:embed firstboot/cleanup.sh
var CleanupSh string

// FixEthNameSh 在首次启动时按 "# mac" 标记修正 interfaces 里的网卡名，
// 避免安装器与目标系统命名规则不同导致网卡无法 ifup。
//
//go:embed firstboot/fix-eth-name.sh
var FixEthNameSh string

// FixEthNameService 是调用 FixEthNameSh 的一次性 systemd 单元，
// 排在 networking.service 之前运行，执行后自删除。
//
//go:embed firstboot/fix-eth-name.service
var FixEthNameService string

// PartmanEarly 是安装器早期按磁盘标识定位设备(并在 keep-data 模式下
// 删除非保留分区)的脚本。
//
//go:embed partman/partman-early.sh
var PartmanEarly string

// InstallerSSHTmpl 是安装器内启动 sshd 的脚本，默认注入。
//
//go:embed installer-ssh/installer-ssh.sh.tmpl
var InstallerSSHTmpl string

// EarlySh 是 preseed 早期命令的统一入口：
// 启动可选的安装器 SSH，并把安全仓库钩子放进 base-installer 的钩子目录。
//
//go:embed installer/early.sh
var EarlySh string

// PostBaseInstallerTmpl 在 base-installer 选择内核之前补上安全仓库并刷新列表，
// 避免先安装主仓库里的旧内核再被 pkgsel 升级。
//
//go:embed installer/post-base-installer.sh.tmpl
var PostBaseInstallerTmpl string

// OptimizeFS 内嵌首次启动时应用系统优化的脚本；文件名前缀决定执行顺序。
//
//go:embed optimize/*.sh
var OptimizeFS embed.FS
