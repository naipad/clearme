package app

// espSizeMB 是 UEFI 系统分区大小；CLI 不暴露，固定 106 MB。
const espSizeMB = 106

// Options 保存解析后的命令行配置。
type Options struct {
	Suite  string
	Mirror string
	China  bool

	IP          string
	Netmask     string
	Gateway     string
	DNS         string
	DNSExplicit bool // 用户显式指定了 --dns
	IPv6        string
	IPv6Gateway string
	IPv6DNS     string
	Hostname    string

	Proxy string

	Username          string
	Password          string
	SSHPort           int
	AuthorizedKeysURL string

	Disk       string
	Filesystem string
	// BtrfsCompress 是 btrfs 根分区的压缩挂载选项，例如 zstd:3；为空表示不压缩。
	BtrfsCompress string
	KeepData      bool
	KeepParts     []string

	Timezone    string
	NTP         string
	Install     string
	Upgrade     string
	Optimize    bool
	Swap        bool
	SwapSize    string
	SwapMB      int
	PostInstall string
	AptRepos    []string
	Unattended  bool
	Fail2ban    bool

	ProgramVersion bool

	Firmware bool

	// InstallerSSH 在安装器环境中启动 sshd，安装期间可远程登录排查。
	// 默认开启，安装结束后随安装器一起消失。
	InstallerSSH bool

	DryRun  bool
	Reboot  bool
	Yes     bool
	Force   bool
	LogFile string
}
