// Package logx 把日志同时写到控制台和文件：控制台带颜色，文件保持纯文本。
package logx

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/naipad/clearme/internal/ui"
)

// Logger 把日志行同时输出到标准输出和文件。
type Logger struct {
	mu   sync.Mutex
	file *os.File
}

// New 创建写入 path 和标准输出的日志器。
func New(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Logger{file: f}, nil
}

// Close 关闭日志文件。
func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// emit 写一行日志：文件始终是纯文本，控制台按级别着色。
func (l *Logger) emit(style func(string) string, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05")

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		fmt.Fprintf(l.file, "[%s] %s\n", ts, msg)
	}
	body := msg
	if style != nil {
		body = style(msg)
	}
	fmt.Fprintf(ui.Stdout(), "%s %s\n", ui.Dim("["+ts+"]"), body)
}

// Info 输出一行普通日志。
func (l *Logger) Info(format string, args ...any) { l.emit(nil, format, args...) }

// Step 输出一行蓝色进度提示。
func (l *Logger) Step(format string, args ...any) { l.emit(ui.Step, format, args...) }

// Title 输出青色标题。
func (l *Logger) Title(format string, args ...any) { l.emit(ui.Title, format, args...) }

// Success 输出绿色成功提示。
func (l *Logger) Success(format string, args ...any) { l.emit(ui.Green, format, args...) }

// Warn 输出黄色警告。
func (l *Logger) Warn(format string, args ...any) { l.emit(ui.Yellow, format, args...) }

// KeyWidth 是摘要键名的对齐宽度(中文按 2 列计算)。
const KeyWidth = 10

// Summary 输出一行“键 值”摘要：键用青色突出，值保持普通样式。
func (l *Logger) Summary(key, format string, args ...any) {
	l.summary(nil, key, format, args...)
}

// SuccessSummary 输出一行绿色强调的摘要，用于随机密码等关键信息。
func (l *Logger) SuccessSummary(key, format string, args ...any) {
	l.summary(ui.Green, key, format, args...)
}

func (l *Logger) summary(style func(string) string, key, format string, args ...any) {
	value := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05")

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		fmt.Fprintf(l.file, "[%s] %s: %s\n", ts, key, value)
	}
	padded := ui.Pad(key, KeyWidth)
	if style != nil {
		fmt.Fprintf(ui.Stdout(), "%s %s  %s\n", ui.Dim("["+ts+"]"), style(padded), style(value))
		return
	}
	fmt.Fprintf(ui.Stdout(), "%s %s  %s\n", ui.Dim("["+ts+"]"), ui.Cyan(padded), value)
}
