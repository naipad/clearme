package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naipad/clearme/internal/logx"
)

// TestBuildKernelArgsDiskID 检查磁盘标识会传给安装器。
func TestBuildKernelArgsDiskID(t *testing.T) {
	got := buildKernelArgs("/dev/vda", []int{2},
		"d6b17c1a-fa1e-40a1-bdcb-0278a3ed9cfc", "abcd-02")
	for _, want := range []string{
		"clearme_disk=/dev/vda",
		"clearme_disk_id=d6b17c1a-fa1e-40a1-bdcb-0278a3ed9cfc",
		"clearme_root_partuuid=abcd-02",
		"clearme_keep=2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("内核参数缺少 %q：%q", want, got)
		}
	}
}

// TestNormalizeDiskID 检查不同工具输出的磁盘标识会被统一。
func TestNormalizeDiskID(t *testing.T) {
	tests := map[string]string{
		" 0xABCDEF12\n":                          "abcdef12",
		"{D6B17C1A-FA1E-40A1-BDCB-0278A3ED9CFC}": "d6b17c1a-fa1e-40a1-bdcb-0278a3ed9cfc",
		"0x36778223":                             "36778223",
	}
	for in, want := range tests {
		if got := normalizeDiskID(in); got != want {
			t.Errorf("normalizeDiskID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResetCommand 检查提示里给出的是可直接执行 reset 的完整命令。
func TestResetCommand(t *testing.T) {
	got := resetCommand()
	if !strings.HasSuffix(got, " reset") {
		t.Fatalf("resetCommand() = %q", got)
	}
	if exe, err := os.Executable(); err == nil && filepath.IsAbs(exe) {
		if want := shellQuote(exe) + " reset"; got != want {
			t.Errorf("resetCommand() = %q, want %q", got, want)
		}
	}
}

// TestShellQuote 检查带空格的路径会被引号包住。
func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"/home/naipad/clearme-amd64": "/home/naipad/clearme-amd64",
		"/opt/my tools/clearme":      "'/opt/my tools/clearme'",
		"/opt/it's/clearme":          `'/opt/it'\''s/clearme'`,
	}
	for in, want := range tests {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDownloadFileFallback 检查校验和失败时会删除文件并改用下一个镜像。
func TestDownloadFileFallback(t *testing.T) {
	content := []byte("installer-kernel")
	good := sha256.Sum256(content)
	bad := sha256.Sum256([]byte("stale"))

	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			fmt.Fprintf(w, "%s  linux\n", hex.EncodeToString(bad[:]))
		case "/linux":
			w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer badSrv.Close()

	goodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			fmt.Fprintf(w, "%s  linux\n", hex.EncodeToString(good[:]))
		case "/linux":
			w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer goodSrv.Close()

	log, err := logx.New(filepath.Join(t.TempDir(), "test.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	dest := filepath.Join(t.TempDir(), "linux")
	if err := newDownloader(log).file(context.Background(), []string{badSrv.URL, goodSrv.URL}, "linux", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("下载内容 = %q", got)
	}
}

// TestDownloadFileAllBad 检查所有镜像都不可用时返回错误。
func TestDownloadFileAllBad(t *testing.T) {
	content := []byte("kernel")
	bad := sha256.Sum256([]byte("stale"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			fmt.Fprintf(w, "%s  linux\n", hex.EncodeToString(bad[:]))
		case "/linux":
			w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	log, err := logx.New(filepath.Join(t.TempDir(), "test.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	if err := newDownloader(log).file(context.Background(), []string{srv.URL}, "linux",
		filepath.Join(t.TempDir(), "linux")); err == nil {
		t.Error("校验和始终不匹配时应当失败")
	}
}

// TestDownloadFileSkipsVerified 检查已存在且校验通过的文件不会重复下载。
func TestDownloadFileSkipsVerified(t *testing.T) {
	content := []byte("installer-kernel")
	sum := sha256.Sum256(content)
	downloads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			fmt.Fprintf(w, "%s  linux\n", hex.EncodeToString(sum[:]))
		case "/linux":
			downloads++
			w.Write(content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	log, err := logx.New(filepath.Join(t.TempDir(), "test.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	dest := filepath.Join(t.TempDir(), "linux")
	if err := os.WriteFile(dest, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := newDownloader(log).file(context.Background(), []string{srv.URL}, "linux", dest); err != nil {
		t.Fatal(err)
	}
	if downloads != 0 {
		t.Errorf("已校验文件不应重新下载，实际下载 %d 次", downloads)
	}
}

// TestBuildInstallList 检查安装阶段会一次装齐用户包、sudo 和自动更新组件。
func TestBuildInstallList(t *testing.T) {
	got := buildInstallList(&Options{Username: "root", Install: "curl", Unattended: true})
	if got != "curl ifupdown unattended-upgrades" {
		t.Errorf("root: %q", got)
	}
	got = buildInstallList(&Options{
		Username: "deploy", Install: "curl sudo", Unattended: true, Fail2ban: true,
	})
	if got != "curl sudo ifupdown unattended-upgrades fail2ban" {
		t.Errorf("普通用户: %q", got)
	}
	// ifupdown 必须始终存在，否则目标系统没有东西去 ifup 网卡。
	if got := buildInstallList(&Options{Username: "root"}); got != "ifupdown" {
		t.Errorf("空列表: %q", got)
	}
}
