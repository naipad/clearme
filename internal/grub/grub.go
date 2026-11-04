// Package grub 负责安装和删除一次性的安装器启动项。
package grub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/naipad/clearme/internal/system"
)

const (
	BeginMarker = "### BEGIN clearme ###"
	EndMarker   = "### END clearme ###"
	MenuTitle   = "Debian Installer (clearme)"
	MenuID      = "clearme"
)

// Entry 描述要添加的 menuentry。
type Entry struct {
	KernelPath string // /boot 内的路径，相对于 boot 分区
	InitrdPath string
	Firmware   string // 可选的第二 initrd
	KernelArgs string
}

// Install 写入自定义启动项并重新生成 grub 配置。
func Install(e Entry) error {
	cfg := "/etc/grub.d/40_custom"
	if _, err := os.Stat(cfg); err != nil {
		return fmt.Errorf("未找到 %s；当前系统可能没有使用 GRUB2", cfg)
	}
	if err := ensureDefaultSaved(); err != nil {
		return err
	}

	block := fmt.Sprintf(`%s
menuentry '%s' --id %s {
    insmod part_msdos
    insmod part_gpt
    insmod ext2
    insmod xfs
    insmod btrfs
    linux %s %s
    initrd %s %s
}
%s
`, BeginMarker, MenuTitle, MenuID, e.KernelPath, e.KernelArgs, e.InitrdPath, e.Firmware, EndMarker)

	data, err := os.ReadFile(cfg)
	if err != nil {
		return err
	}
	cleaned := stripBlock(string(data))
	if err := os.WriteFile(cfg, []byte(cleaned+block), 0o755); err != nil {
		return err
	}
	return Update()
}

// Reset 删除启动项和已下载的安装器文件。
func Reset() error {
	cfg := "/etc/grub.d/40_custom"
	if data, err := os.ReadFile(cfg); err == nil {
		if err := os.WriteFile(cfg, []byte(stripBlock(string(data))), 0o755); err != nil {
			return err
		}
	}
	os.Remove("/etc/default/grub.d/zz-clearme.cfg")
	os.RemoveAll("/boot/clearme")
	clearNextEntry()
	if err := Update(); err != nil {
		return err
	}
	return nil
}

// Installed 报告 /etc/grub.d/40_custom 中是否已有 clearme 启动项。
func Installed() bool {
	data, err := os.ReadFile("/etc/grub.d/40_custom")
	return err == nil && strings.Contains(string(data), BeginMarker)
}

// NextEntry 返回 grubenv 中记录的一次性启动项；没有时返回空字符串。
func NextEntry() string {
	for _, env := range []string{"/boot/grub/grubenv", "/boot/grub2/grubenv"} {
		data, err := os.ReadFile(env)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimRight(line, "#\r")
			k, v, ok := strings.Cut(line, "=")
			if ok && k == "next_entry" {
				return v
			}
		}
	}
	return ""
}

// clearNextEntry 清除一次性启动目标，让 reset 后的普通重启回到当前系统。
func clearNextEntry() {
	for _, env := range []string{"/boot/grub/grubenv", "/boot/grub2/grubenv"} {
		if _, err := os.Stat(env); err != nil {
			continue
		}
		if tool := system.FirstExisting("grub-editenv", "grub2-editenv"); tool != "" {
			system.RunNoExit(tool, env, "unset", "next_entry")
		}
	}
}

func stripBlock(s string) string {
	start := strings.Index(s, BeginMarker)
	if start < 0 {
		return s
	}
	end := strings.Index(s[start:], EndMarker)
	if end < 0 {
		return s[:start]
	}
	end += start + len(EndMarker)
	// 连同结束标记所在行的换行一起删除。
	if end < len(s) && s[end] == '\n' {
		end++
	}
	return s[:start] + s[end:]
}

func ensureDefaultSaved() error {
	dir := "/etc/default/grub.d"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := "# Added by clearme\nGRUB_DEFAULT=saved\nGRUB_TIMEOUT=5\nGRUB_TIMEOUT_STYLE=menu\n"
	return os.WriteFile(filepath.Join(dir, "zz-clearme.cfg"), []byte(content), 0o644)
}

// Update 使用系统中可用的工具重新生成 grub.cfg。
func Update() error {
	type tool struct {
		name string
		args []string
	}
	tools := []tool{
		{"update-grub", nil},
		{"grub2-mkconfig", []string{"-o", "/boot/grub2/grub.cfg"}},
		{"grub-mkconfig", []string{"-o", "/boot/grub/grub.cfg"}},
	}
	var lastErr error
	for _, t := range tools {
		if !system.Exists(t.name) {
			continue
		}
		out, err := system.Run(t.name, t.args...)
		if err == nil {
			return nil
		}
		lastErr = system.ErrCommand(t.name, err, out)
	}
	if lastErr == nil {
		return fmt.Errorf("未找到可用的 GRUB 配置工具(update-grub/grub2-mkconfig/grub-mkconfig)")
	}
	return lastErr
}

// SetOneTimeBoot 让 grub 只在下次启动时进入安装器。
func SetOneTimeBoot() error {
	name := system.FirstExisting("grub-reboot", "grub2-reboot")
	if name == "" {
		return fmt.Errorf("未找到 grub-reboot；请重启并手动选择 %q", MenuTitle)
	}
	out, err := system.Run(name, MenuTitle)
	if err != nil {
		return system.ErrCommand(name, err, out)
	}
	return nil
}

// RelPath 把 /boot 下的绝对路径转换为 grub 使用的相对路径。
func RelPath(p string) (string, error) {
	for _, name := range []string{"grub-mkrelpath", "grub2-mkrelpath"} {
		if system.Exists(name) {
			out, err := system.Run(name, p)
			if err == nil && strings.HasPrefix(out, "/") {
				return out, nil
			}
		}
	}
	// 回退方案：处理 /boot 单独分区的情况。
	if bootDev, err := system.Output("findmnt", "-n", "-o", "SOURCE", "/boot"); err == nil && bootDev != "" {
		rootDev, _ := system.Output("findmnt", "-n", "-o", "SOURCE", "/")
		if bootDev != rootDev {
			return strings.TrimPrefix(p, "/boot"), nil
		}
	}
	return p, nil
}
