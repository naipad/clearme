package initrd

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/naipad/clearme/internal/cpio"
)

func TestAppendCpio(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "initrd.gz")
	orig := []byte("original-initramfs-bytes")

	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(orig)
	w.Close()
	if err := os.WriteFile(in, gz.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	entries := []cpio.Entry{cpio.File("preseed.cfg", 0644, []byte("preseed"))}
	if err := AppendCpio(in, in, entries); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, orig) {
		t.Fatal("original initramfs bytes were not preserved")
	}
	if !bytes.Contains(raw, []byte("preseed.cfg")) {
		t.Fatal("cpio entry not appended")
	}
}

// TestNames 检查能读出 cpio 里的已有路径，包括符号链接和嵌套目录。
func TestNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initrd.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	entries := []cpio.Entry{
		cpio.File("usr/bin/sshd", 0755, []byte("sshd")),
		cpio.Symlink("usr/lib/libc.so.6", "libc.so.6.1"),
		cpio.File("clearme-assets/late.sh", 0755, []byte("#!/bin/sh\n")),
	}
	if err := cpio.Write(zw, entries); err != nil {
		t.Fatal(err)
	}
	// 模拟 AppendCpio：在 TRAILER 之后再追加一个归档。
	if err := cpio.Write(zw, []cpio.Entry{cpio.File("usr/sbin/sshd", 0755, []byte("sshd"))}); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	names, err := Names(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"usr", "usr/bin", "usr/bin/sshd", "usr/lib/libc.so.6", "clearme-assets/late.sh"} {
		if !names[want] {
			t.Errorf("缺少 %q：%v", want, names)
		}
	}
	if names["TRAILER!!!"] {
		t.Error("不应包含 TRAILER 条目")
	}
}
