package app

import (
	"context"
	"fmt"
	"time"

	"github.com/naipad/clearme/internal/logx"
	"github.com/naipad/clearme/internal/mirror"
	"github.com/naipad/clearme/internal/release"
)

// resolveSuite 解析目标版本。未指定 --version 或指定 stable 时，
// 读取镜像上 dists/stable/Release 的代号，跟随当前 stable，
// 这样新版本发布后不需要重新编译也能安装最新的稳定版。
func resolveSuite(o *Options, log *logx.Logger) (release.Info, error) {
	if !release.StableAlias(o.Suite) {
		return release.Resolve(o.Suite)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var lastErr error
	for _, m := range stableMirrors(ctx, o) {
		text, err := mirror.FetchText(ctx, m.ReleaseURL("stable"), 1<<20)
		if err != nil {
			lastErr = err
			continue
		}
		info, ok := release.ParseStable(text)
		if !ok {
			lastErr = fmt.Errorf("%s 的 dists/stable/Release 无法解析", m.Host)
			continue
		}
		if log != nil {
			log.Info("stable 当前为 Debian %s (%s)", release.Display(info.Suite), info.Suite)
		}
		return info, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的镜像")
	}
	return release.Info{}, fmt.Errorf("无法确定当前 stable 版本：%w；请用 --version 指定版本", lastErr)
}

// stableMirrors 返回读取 stable Release 时依次尝试的镜像。
// --china 先对国内镜像测速，其余情况按用户镜像/官方镜像的优先级排列。
func stableMirrors(ctx context.Context, o *Options) []mirror.Mirror {
	if o.China {
		return append(mirror.ByLatency(ctx, mirror.ChinaMirrors, "stable"), mirror.Candidates(nil)...)
	}
	return mirror.Candidates(userMirror(o))
}
