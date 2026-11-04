//go:build windows

package ui

import "golang.org/x/sys/windows"

// 旧版 Windows 控制台默认使用本地代码页(如 936)，
// 会把 Go 输出的 UTF-8 中文显示成乱码；启动时尽量切换到 UTF-8。
func init() {
	_ = windows.SetConsoleOutputCP(65001)
}
