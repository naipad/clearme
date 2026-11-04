package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/naipad/clearme/internal/grub"
	"github.com/naipad/clearme/internal/humanize"
	"github.com/naipad/clearme/internal/ui"
	"github.com/naipad/clearme/internal/version"
)

// statusFile 描述安装器相关的一个文件。
type statusFile struct {
	path string
	name string
}

// Status 打印当前机器上的 clearme 状态：安装文件、GRUB 启动项和日志。
func Status() error {
	fmt.Fprintf(ui.Stdout(), "%s %s\n\n", ui.Title("clearme"), ui.Dim(version.Version))

	files := []statusFile{
		{"/boot/clearme/linux", "内核"},
		{"/boot/clearme/initrd.gz", "initrd"},
		{"/boot/clearme/firmware.cpio.gz", "固件"},
	}
	found := false
	for _, f := range files {
		st, err := os.Stat(f.path)
		if err != nil {
			continue
		}
		found = true
		ui.Printf("  %-10s %s(%s)", f.name, f.path, humanize.Bytes(st.Size()))
	}
	if !found {
		ui.Printf("  %-10s %s", "安装文件", "未准备")
	}

	if grub.Installed() {
		ui.Printf("  %-10s %s", "GRUB 启动项", "已写入 /etc/grub.d/40_custom")
	} else {
		ui.Printf("  %-10s %s", "GRUB 启动项", "未写入")
	}
	if next := grub.NextEntry(); next != "" {
		ui.Printf("  %-10s %s(仅下次启动)", "下次启动", next)
	} else {
		ui.Printf("  %-10s %s", "下次启动", "正常启动")
	}

	if _, err := os.Stat("/etc/systemd/system/clearme-firstboot.service"); err == nil {
		ui.Printf("  %-10s %s", "首启服务", "已安装(重启进入新系统后执行)")
	}
	for _, log := range logFiles() {
		if _, err := os.Stat(log.path); err == nil {
			ui.Printf("  %-10s %s", "日志", log.path)
		}
	}
	return nil
}

// logFile 描述一个 clearme 日志。
type logFile struct {
	kind string
	path string
}

func logFiles() []logFile {
	return []logFile{
		{"", "/var/log/clearme.log"},
		{"firstboot", "/var/log/clearme-firstboot.log"},
		{"cleanup", "/var/log/clearme-cleanup.log"},
	}
}

// Logs 输出 clearme 日志的最后 80 行。kind 可选 firstboot、cleanup、all。
func Logs(kind string) error {
	var selected []logFile
	switch kind {
	case "", "clearme":
		selected = logFiles()[:1]
	case "firstboot", "cleanup":
		for _, f := range logFiles() {
			if f.kind == kind {
				selected = append(selected, f)
			}
		}
	case "all":
		selected = logFiles()
	default:
		return fmt.Errorf("未知日志类型 %q(可选：clearme、firstboot、cleanup、all)", kind)
	}

	printed := false
	for _, f := range selected {
		data, err := tailFile(f.path, 80, 512<<10)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if os.IsPermission(err) {
				ui.Warnf("无法读取 %s(需要 root)", f.path)
				continue
			}
			return err
		}
		printed = true
		ui.Titlef("==> %s <==", f.path)
		fmt.Fprint(ui.Stdout(), data)
		if !strings.HasSuffix(data, "\n") {
			fmt.Fprintln(ui.Stdout())
		}
	}
	if !printed {
		ui.Printf("没有找到 clearme 日志(安装阶段会写入 /var/log/clearme.log)")
	}
	return nil
}

// tailFile 返回文件末尾最多 maxBytes 内的最后 n 行。
func tailFile(path string, n int, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := int64(0)
	if st.Size() > maxBytes {
		start = st.Size() - maxBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n", nil
}
