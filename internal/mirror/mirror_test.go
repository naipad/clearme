package mirror

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseChecksums(t *testing.T) {
	text := strings.Join([]string{
		"# comment",
		strings.Repeat("a", 64) + "  linux",
		strings.Repeat("B", 64) + " *./initrd.gz",
		"not-a-hash  ignored",
		strings.Repeat("c", 63) + "  too-short",
	}, "\n")
	sums := ParseChecksums(text)
	if got := sums["linux"]; got != strings.Repeat("a", 64) {
		t.Errorf("linux = %q", got)
	}
	if got := sums["initrd.gz"]; got != strings.Repeat("b", 64) {
		t.Errorf("initrd.gz = %q", got)
	}
	if len(sums) != 2 {
		t.Errorf("解析到 %d 项，want 2", len(sums))
	}
}

func TestChecksumCandidates(t *testing.T) {
	base := "https://deb.debian.org/debian/dists/trixie/main/installer-amd64/current/images/netboot/debian-installer/amd64"
	got := ChecksumCandidates(base)
	want := "https://deb.debian.org/debian/dists/trixie/main/installer-amd64/current/images/SHA256SUMS"
	if len(got) != 2 || got[0] != want {
		t.Errorf("ChecksumCandidates() = %v, want 第一项 %s", got, want)
	}

	daily := "https://d-i.debian.org/daily-images/amd64/daily/netboot/debian-installer/amd64"
	if got := ChecksumCandidates(daily); got[0] != "https://d-i.debian.org/daily-images/amd64/daily/SHA256SUMS" {
		t.Errorf("每日构建校验和地址 = %v", got)
	}
}

// TestByLatency 检查并发测速会排除不可用镜像并按响应时间排序。
func TestByLatency(t *testing.T) {
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("release"))
	}))
	defer fast.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("release"))
	}))
	defer slow.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()

	toMirror := func(raw string) Mirror {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return Mirror{Protocol: u.Scheme, Host: u.Host, Directory: "/debian"}
	}
	fastM, slowM := toMirror(fast.URL), toMirror(slow.URL)
	if got := fastM.ReleaseURL("trixie"); got != fast.URL+"/debian/dists/trixie/Release" {
		t.Errorf("ReleaseURL = %q", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := ByLatency(ctx, []Mirror{slowM, toMirror(dead.URL), fastM}, "trixie")
	if len(got) != 2 {
		t.Fatalf("ByLatency 返回 %d 个镜像，want 2：%v", len(got), got)
	}
	if got[0] != fastM {
		t.Errorf("最快的镜像 = %v，want %v", got[0], fastM)
	}
}

func TestSelectNetbootSums(t *testing.T) {
	sums := ParseChecksums(strings.Join([]string{
		strings.Repeat("a", 64) + "  ./cdrom/linux",
		strings.Repeat("b", 64) + "  ./netboot/debian-installer/amd64/linux",
		strings.Repeat("c", 64) + "  ./netboot/debian-installer/amd64/initrd.gz",
		strings.Repeat("e", 64) + "  ./netboot/gtk/debian-installer/amd64/linux",
	}, "\n"))
	got := selectNetbootSums(sums, "netboot/debian-installer/amd64")
	if got["linux"] != strings.Repeat("b", 64) {
		t.Errorf("按文件名查找 linux = %q，应取 debian-installer 条目", got["linux"])
	}
	if _, ok := got["netboot/gtk/debian-installer/amd64/linux"]; ok {
		t.Error("gtk 条目应被过滤")
	}
	if _, ok := got["cdrom/linux"]; ok {
		t.Error("非 netboot 条目应被过滤")
	}
	if got["netboot/debian-installer/amd64/linux"] != strings.Repeat("b", 64) {
		t.Error("完整路径键丢失")
	}
}

func TestVerifyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("clearme"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("clearme"))
	if err := VerifyFile(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("VerifyFile() = %v", err)
	}
	if err := VerifyFile(path, strings.Repeat("0", 64)); err == nil {
		t.Error("错误的校验和应当失败")
	}
}

func TestDownloadResume(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789"), 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "linux", time.Now(), bytes.NewReader(content))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "linux")
	if err := os.WriteFile(dest+".part", content[:1234], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), srv.URL+"/linux", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("断点续传结果不一致：got %d bytes, want %d", len(got), len(content))
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error(".part 应当在完成后被重命名")
	}
}

func TestSetProxy(t *testing.T) {
	defer SetProxy("")
	if err := SetProxy("http://127.0.0.1:3128"); err != nil {
		t.Fatal(err)
	}
	if err := SetProxy("ftp://proxy:21"); err == nil {
		t.Error("不支持的代理协议应当报错")
	}
	if err := SetProxy("http://"); err == nil {
		t.Error("缺少主机名应当报错")
	}
}
