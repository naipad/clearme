// Package version 保存程序的版本号。
package version

// Version 是 clearme 的发布版本号。
// 发布构建通过 -ldflags "-X github.com/naipad/clearme/internal/version.Version=..."
// 注入 tag 版本；源码构建使用下面的默认值。
var Version = "0.1.2"
