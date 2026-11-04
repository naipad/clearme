package app

import (
	"context"
	"time"

	"github.com/naipad/clearme/internal/keepdata"
	"github.com/naipad/clearme/internal/mirror"
	"github.com/naipad/clearme/internal/netconf"
	"github.com/naipad/clearme/internal/release"
)

// DryRun 只渲染配置摘要，不下载安装器、不修改系统。
// 跟随 stable 时会读取一次 Release 文件；无法联网时按 Debian 13 预览。
func DryRun(o *Options) error {
	info, err := resolveSuite(o, nil)
	suiteNote := ""
	if err != nil {
		if !release.StableAlias(o.Suite) {
			return err
		}
		info, _ = release.Resolve("13")
		suiteNote = "无法读取 stable 版本，预览按 Debian 13 显示"
	}
	suite := info.Suite
	daily := info.Daily
	arch, _ := targetArch()
	if arch == "" {
		arch = "amd64"
	}

	// 网络探测只读；失败时不影响 dry-run。
	n, _, err := resolveNetwork(o, info.Number, nil)
	if err != nil {
		n = netconf.Net{}
	}

	disk := o.Disk
	diskNote := ""
	if disk == "" {
		if d, err := resolveDisk(o, nil); err == nil {
			disk = d
		} else {
			disk = "/dev/sda"
			diskNote = "示例值"
		}
	}
	efi := detectEFI()
	hostname := resolveHostname(o)

	// dry-run 不探测真实分区；--keep-part 直接展示，否则用示例值。
	var keepParts []int
	mounts := map[int]string{}
	if o.KeepData {
		if len(o.KeepParts) == 0 {
			keepParts = []int{2}
		}
		for _, p := range o.KeepParts {
			if num := keepdata.PartNum(p); num > 0 {
				keepParts = append(keepParts, num)
			}
		}
		for i, num := range keepParts {
			mounts[num] = keepdata.MountPointFor(i)
		}
	}

	// --china 也做同样的测速，保证预览显示的镜像和实际一致。
	m := mirror.Mirror{Protocol: "https", Host: "deb.debian.org", Directory: "/debian"}
	if o.China {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if ms := mirror.ByLatency(ctx, mirror.ChinaMirrors, suite); len(ms) > 0 {
			m = ms[0]
		}
		cancel()
	} else if user := userMirror(o); user != nil {
		m = *user
	}
	securityRepo := release.SecurityRepo(m.Protocol, m.Host, m.Directory, suite, daily)

	printDryRunSummary(o, suite, arch, disk, diskNote, suiteNote, efi, n, hostname, m, securityRepo, keepParts, mounts)
	return nil
}
