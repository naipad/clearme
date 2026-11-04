// Package ui 统一终端彩色输出；输出被重定向或设置 NO_COLOR 时自动退化为纯文本。
package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
)

// 样式集中定义，避免各文件散落 ANSI 转义序列。
var (
	titleStyle  = color.New(color.FgCyan, color.Bold)
	keyStyle    = color.New(color.FgHiCyan)
	boldStyle   = color.New(color.Bold)
	dimStyle    = color.New(color.Faint)
	greenStyle  = color.New(color.FgGreen, color.Bold)
	yellowStyle = color.New(color.FgYellow, color.Bold)
	redStyle    = color.New(color.FgRed, color.Bold)
	stepStyle   = color.New(color.FgHiBlue)
)

// IsTerminal 报告标准输出是否连接终端(重定向或 NO_COLOR 时为 false)。
func IsTerminal() bool { return !color.NoColor }

// Stdout 返回适合写标准输出的彩色 writer。
func Stdout() io.Writer { return color.Output }

// Stderr 返回适合写标准错误的彩色 writer。
func Stderr() io.Writer { return color.Error }

// Title 返回青色加粗文本，用于标题。
func Title(s string) string { return titleStyle.Sprint(s) }

// Bold 返回加粗文本。
func Bold(s string) string { return boldStyle.Sprint(s) }

// Dim 返回灰色文本，用于时间戳等次要信息。
func Dim(s string) string { return dimStyle.Sprint(s) }

// Cyan 返回青色文本，用于标题和键名。
func Cyan(s string) string { return keyStyle.Sprint(s) }

// Green 返回绿色文本，用于成功信息。
func Green(s string) string { return greenStyle.Sprint(s) }

// Yellow 返回黄色文本，用于警告。
func Yellow(s string) string { return yellowStyle.Sprint(s) }

// Step 返回蓝色文本，用于“正在...”之类的进度提示。
func Step(s string) string { return stepStyle.Sprint(s) }

// Printf 向标准输出打印一行普通信息。
func Printf(format string, args ...any) {
	fmt.Fprintf(color.Output, format+"\n", args...)
}

// Titlef 向标准输出打印一行青色标题。
func Titlef(format string, args ...any) {
	titleStyle.Fprintf(color.Output, format+"\n", args...)
}

// Successf 向标准输出打印一行绿色成功信息。
func Successf(format string, args ...any) {
	greenStyle.Fprintf(color.Output, format+"\n", args...)
}

// Warnf 向标准输出打印一行黄色警告。
func Warnf(format string, args ...any) {
	yellowStyle.Fprintf(color.Output, format+"\n", args...)
}

// Errorf 向标准错误打印一行红色错误。
func Errorf(format string, args ...any) {
	redStyle.Fprintf(color.Error, format+"\n", args...)
}

// Width 返回字符串在终端里的显示宽度；中日韩全角字符按 2 列计算。
func Width(s string) int {
	w := 0
	for _, r := range s {
		if isWide(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// Pad 在右侧补空格，使字符串的显示宽度达到 width。
func Pad(s string, width int) string {
	if d := width - Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// isWide 判断 rune 是否属于占用两个终端列的东亚宽字符。
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE4F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F,
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}
