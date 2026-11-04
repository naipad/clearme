// Package guard 检查当前主机是否允许执行重装。
package guard

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/naipad/clearme/internal/system"
)

// Result 保存非致命的警告信息。
type Result struct {
	Warnings []string
}

// Check 执行环境检查；force 会放宽致命检查。
func Check(force bool) (Result, error) {
	var res Result
	if !system.IsRoot() {
		return res, fmt.Errorf("需要 root 权限")
	}
	if err := checkContainer(); err != nil {
		if !force {
			return res, err
		}
		res.Warnings = append(res.Warnings, err.Error()+"(已用 --force 忽略)")
	}
	if isWSL() {
		if !force {
			return res, fmt.Errorf("不支持 WSL 环境")
		}
		res.Warnings = append(res.Warnings, "检测到 WSL(已用 --force 忽略)")
	}
	if secureBoot() {
		if !force {
			return res, fmt.Errorf("已启用 Secure Boot，安装器内核无法启动；请先关闭 Secure Boot")
		}
		res.Warnings = append(res.Warnings, "已启用 Secure Boot(已用 --force 忽略)")
	}
	if memMB() > 0 && memMB() < 384 {
		if !force {
			return res, fmt.Errorf("仅检测到 %d MB 内存，至少需要 384 MB", memMB())
		}
		res.Warnings = append(res.Warnings, fmt.Sprintf("内存偏低：%d MB", memMB()))
	}
	if id := osReleaseID(); id != "debian" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("源系统是 %q，不是 Debian；仍继续执行", id))
	}
	return res, nil
}

func checkContainer() error {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("正在容器(%s)内运行，无法修改引导器", p)
		}
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(data)
		for _, marker := range []string{"docker", "lxc", "kubepods", "containerd"} {
			if strings.Contains(s, marker) {
				return fmt.Errorf("正在容器(%s)内运行，无法修改引导器", marker)
			}
		}
	}
	if _, err := os.Stat("/proc/vz"); err == nil {
		if _, err := os.Stat("/proc/bc"); err == nil {
			return fmt.Errorf("检测到 OpenVZ 容器，无法修改引导器")
		}
	}
	if system.Exists("systemd-detect-virt") {
		if out, err := system.Output("systemd-detect-virt"); err == nil {
			switch out {
			case "openvz", "lxc", "lxc-libvirt", "docker", "podman", "systemd-nspawn":
				return fmt.Errorf("虚拟化环境 %q 不允许重装系统", out)
			}
		}
	}
	return nil
}

func isWSL() bool {
	data, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func secureBoot() bool {
	entries, err := os.ReadDir("/sys/firmware/efi/efivars")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "SecureBoot-") {
			data, err := os.ReadFile("/sys/firmware/efi/efivars/" + e.Name())
			if err == nil && len(data) > 4 {
				return data[4] == 1
			}
		}
	}
	return false
}

func memMB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return int(kb / 1024)
		}
	}
	return 0
}

func osReleaseID() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ID=") {
			return strings.Trim(strings.TrimPrefix(line, "ID="), `"`)
		}
	}
	return ""
}

// MemoryMB 对外暴露内存检测结果。
func MemoryMB() int { return memMB() }
