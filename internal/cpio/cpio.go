// Package cpio 生成 initramfs 使用的 newc(SVR4)cpio 归档。
package cpio

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Entry 表示 cpio 归档中的一个文件。
type Entry struct {
	Name string // 相对路径，不能以 / 开头
	Mode uint32 // Unix 权限位，例如 0644、0755，或目录的 0040755
	Data []byte
}

// Dir 返回一个目录项。
func Dir(name string, mode uint32) Entry {
	return Entry{Name: name, Mode: mode | 0040000}
}

// File 返回一个普通文件项。
func File(name string, mode uint32, data []byte) Entry {
	return Entry{Name: name, Mode: mode | 0100000, Data: data}
}

// Symlink 返回一个符号链接项，data 保存链接目标。
func Symlink(name, target string) Entry {
	return Entry{Name: name, Mode: 0120777, Data: []byte(target)}
}

// Write 把 entries 写成 newc 归档。
func Write(w io.Writer, entries []Entry) error {
	entries = withParents(entries)
	ino := uint32(1)
	for _, e := range entries {
		if err := writeEntry(w, e, ino); err != nil {
			return err
		}
		ino++
	}
	// 归档结束标记。
	return writeEntry(w, Entry{Name: "TRAILER!!!", Mode: 0}, 0)
}

func withParents(entries []Entry) []Entry {
	have := map[string]bool{}
	for _, e := range entries {
		have[strings.TrimSuffix(e.Name, "/")] = true
	}
	var dirs []Entry
	for _, e := range entries {
		parts := strings.Split(strings.Trim(e.Name, "/"), "/")
		for i := 1; i < len(parts); i++ {
			d := strings.Join(parts[:i], "/")
			if !have[d] {
				have[d] = true
				dirs = append(dirs, Dir(d, 0755))
			}
		}
	}
	all := append(dirs, entries...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

func writeEntry(w io.Writer, e Entry, ino uint32) error {
	name := []byte(e.Name)
	var size int
	if e.Mode&0040000 == 0 {
		size = len(e.Data)
	}
	hdr := fmt.Sprintf("070701%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X",
		ino, e.Mode, 0, 0, 1, 0, size, 0, 0, 0, 0, len(name)+1, 0)
	if _, err := io.WriteString(w, hdr); err != nil {
		return err
	}
	if _, err := w.Write(name); err != nil {
		return err
	}
	if _, err := w.Write([]byte{0}); err != nil {
		return err
	}
	if err := pad(w, 110+len(name)+1); err != nil {
		return err
	}
	if size > 0 {
		if _, err := w.Write(e.Data); err != nil {
			return err
		}
		if err := pad(w, size); err != nil {
			return err
		}
	}
	return nil
}

func pad(w io.Writer, n int) error {
	if r := n % 4; r != 0 {
		_, err := w.Write(make([]byte, 4-r))
		return err
	}
	return nil
}
