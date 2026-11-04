// Package initrd 负责解压、追加文件和重新压缩 Debian 安装器的 initrd。
package initrd

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"

	"github.com/naipad/clearme/internal/cpio"
)

// AppendCpio 在重新压缩 gzipPath 的同时追加 entries，结果以 gzip 写入 outPath。
// 全程流式处理，不会把解压后的完整 initrd 写入磁盘或读入内存。
func AppendCpio(gzipPath, outPath string, entries []cpio.Entry) error {
	in, err := os.Open(gzipPath)
	if err != nil {
		return err
	}
	defer in.Close()

	zr, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("initrd 不是 gzip 格式：%w", err)
	}
	defer zr.Close()

	tmpPath := outPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	zw.Name = ""

	if _, err := io.Copy(zw, zr); err != nil {
		zw.Close()
		out.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := cpio.Write(zw, entries); err != nil {
		zw.Close()
		out.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	// 重命名前先关闭输入文件：Windows 不允许替换仍被打开的文件，
	// 而且调用方可能对同一个路径同时作为输入和输出。
	zr.Close()
	in.Close()
	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
