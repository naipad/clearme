// Package keepdata 找出重装后需要保留的数据分区。
package keepdata

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/naipad/clearme/internal/system"
)

// Part 是目标磁盘上的一个分区。
type Part struct {
	Name       string // sda1
	Path       string // /dev/sda1
	Num        int
	Size       string
	FSType     string
	Mountpoint string
	PartUUID   string
}

// List 返回 disk(例如 /dev/sda)上的所有分区。
func List(disk string) ([]Part, error) {
	out, err := system.Output("lsblk", "-P", "-p", "-o",
		"NAME,TYPE,SIZE,FSTYPE,MOUNTPOINT,PARTUUID", disk)
	if err != nil {
		return nil, system.ErrCommand("lsblk", err, out)
	}
	var parts []Part
	for _, line := range strings.Split(out, "\n") {
		fields := parsePairs(line)
		if fields["TYPE"] != "part" {
			continue
		}
		path := fields["NAME"]
		p := Part{
			Path:       path,
			Name:       strings.TrimPrefix(path, "/dev/"),
			FSType:     fields["FSTYPE"],
			Mountpoint: fields["MOUNTPOINT"],
			Size:       fields["SIZE"],
			PartUUID:   fields["PARTUUID"],
		}
		p.Num = PartNum(p.Name)
		if p.Num > 0 {
			parts = append(parts, p)
		}
	}
	return parts, nil
}

// parsePairs 解析 lsblk -P 的 `KEY="value" KEY="value"` 输出。
func parsePairs(line string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(line) {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		start := i
		for i < len(line) && line[i] != '=' {
			i++
		}
		if i >= len(line) {
			break
		}
		key := line[start:i]
		i++ // skip =
		i++ // skip opening quote
		valStart := i
		for i < len(line) && line[i] != '"' {
			i++
		}
		val := line[valStart:i]
		i++ // skip closing quote
		out[key] = val
	}
	return out
}

// PartNum 返回设备名末尾的分区号，例如 sda3 或 /dev/sda3 都得到 3；没有数字时返回 0。
func PartNum(name string) int {
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(name[i:])
	return n
}

// IsMountable 报告 fstype 是否属于默认可挂载的文件系统。
func IsMountable(fstype string) bool {
	switch fstype {
	case "ext2", "ext3", "ext4", "xfs", "btrfs":
		return true
	}
	return false
}

// SystemMounts 是旧系统占用的挂载点。
var SystemMounts = map[string]bool{
	"/": true, "/boot": true, "/boot/efi": true, "/usr": true,
	"/var": true, "/etc": true, "/tmp": true, "/opt": false,
}

// Analyze 选择需要保留的分区。
// explicit 是用户指定的设备路径(可以为空)。
// rootPath 是挂载在 / 的设备。
func Analyze(disk, rootPath string, explicit []string) ([]Part, error) {
	parts, err := List(disk)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Part{}
	for _, p := range parts {
		byPath[p.Path] = p
	}

	if len(explicit) > 0 {
		var keep []Part
		for _, path := range explicit {
			p, ok := byPath[path]
			if !ok {
				return nil, fmt.Errorf("分区 %s 不属于 %s", path, disk)
			}
			if err := validate(p, rootPath); err != nil {
				return nil, err
			}
			keep = append(keep, p)
		}
		return keep, nil
	}

	var candidates []Part
	for _, p := range parts {
		if p.Path == rootPath {
			continue
		}
		if p.FSType == "swap" || p.FSType == "vfat" || p.FSType == "" {
			continue
		}
		if p.Mountpoint != "" && SystemMounts[p.Mountpoint] {
			continue
		}
		if !IsMountable(p.FSType) {
			continue
		}
		candidates = append(candidates, p)
	}
	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("--keep-data：%s 上没有找到数据分区；请用 --keep-part /dev/xxx 指定", disk)
	case 1:
		return candidates, nil
	default:
		var names []string
		for _, p := range candidates {
			names = append(names, fmt.Sprintf("%s (%s %s)", p.Path, p.FSType, p.Size))
		}
		return nil, fmt.Errorf("--keep-data：找到多个候选分区：%s；请用 --keep-part 选择",
			strings.Join(names, ", "))
	}
}

func validate(p Part, rootPath string) error {
	if p.Path == rootPath {
		return fmt.Errorf("不能保留根分区 %s", p.Path)
	}
	if p.FSType == "swap" {
		return fmt.Errorf("不能保留 swap 分区 %s", p.Path)
	}
	if p.Mountpoint != "" && SystemMounts[p.Mountpoint] {
		return fmt.Errorf("不能保留 %s：它挂载在系统路径 %s", p.Path, p.Mountpoint)
	}
	if p.FSType == "" {
		return fmt.Errorf("不能保留 %s：未知文件系统", p.Path)
	}
	return nil
}

// MountPointFor 返回第 i 个保留分区的挂载点。
func MountPointFor(i int) string {
	if i == 0 {
		return "/data"
	}
	return fmt.Sprintf("/data%d", i+1)
}

// FstabPass 返回 fstype 对应的 fsck pass 值。
func FstabPass(fstype string) int {
	if fstype == "btrfs" {
		return 0
	}
	return 2
}
