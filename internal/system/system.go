// Package system 提供对常用外部命令的小封装。
package system

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Run 执行 name 并返回合并后的 stdout/stderr。
func Run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Output 执行 name，只返回 stdout。
func Output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// OutputTimeout 与 Output 相同，但超时后终止命令。
func OutputTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Exists 报告 name 是否存在于 PATH 中。
func Exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// IsRoot 报告当前进程是否以 uid 0 运行。
func IsRoot() bool {
	out, err := Output("id", "-u")
	return err == nil && strings.TrimSpace(out) == "0"
}

// FirstExisting 返回第一个存在的命令，全部不存在时返回空字符串。
func FirstExisting(names ...string) string {
	for _, n := range names {
		if Exists(n) {
			return n
		}
	}
	return ""
}

// RunNoExit 执行命令并接入父进程的 stdio。
func RunNoExit(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// ErrCommand 为执行失败的命令生成带输出的错误。
func ErrCommand(name string, err error, out string) error {
	if out == "" {
		return fmt.Errorf("%s: %w", name, err)
	}
	return fmt.Errorf("%s: %w: %s", name, err, out)
}
