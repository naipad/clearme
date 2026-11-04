package diudeb

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"
)

func TestParseIndex(t *testing.T) {
	const body = `Package: openssh-server-udeb
Version: 1:10.0p1-7
Depends: libc6-udeb (>= 2.41), libcrypto3-udeb (>= 3.5.6) | libssl3-udeb,
 zlib1g-udeb
Filename: pool/main/o/openssh/openssh-server-udeb_10_amd64.udeb
SHA256: aabb

Package: libc6-udeb
Filename: pool/main/g/glibc/libc6-udeb_2.41_amd64.udeb

Package: libcrypto3-udeb
Depends: libc6-udeb
Filename: pool/main/o/openssl/libcrypto3-udeb_3.5.7_amd64.udeb

Package: libssl3-udeb
Filename: pool/main/o/openssl/libssl3-udeb.udeb

Package: zlib1g-udeb
Filename: pool/main/z/zlib/zlib1g-udeb.udeb
`
	index, err := parseIndex(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 5 {
		t.Fatalf("解析出 %d 个包，want 5", len(index))
	}
	p := index[SSHServer]
	if p == nil || p.Filename == "" || p.SHA256 != "aabb" {
		t.Fatalf("openssh-server-udeb 字段不完整：%+v", p)
	}
	if len(p.Depends) != 3 {
		t.Fatalf("依赖组数 = %d, want 3：%v", len(p.Depends), p.Depends)
	}
	if got := p.Depends[1]; len(got) != 2 || got[0] != "libcrypto3-udeb" || got[1] != "libssl3-udeb" {
		t.Errorf("备选依赖解析错误：%v", got)
	}

	order, err := resolve(index, []string{SSHServer})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) == 0 || order[0] != SSHServer {
		t.Fatalf("根包应排在最前：%v", order)
	}
	for _, want := range []string{"libc6-udeb", "libcrypto3-udeb", "zlib1g-udeb"} {
		found := false
		for _, name := range order {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("依赖闭包缺少 %s：%v", want, order)
		}
	}
	for _, name := range order {
		if name == "libssl3-udeb" {
			t.Errorf("已有 libcrypto3-udeb 时不应再选备选依赖：%v", order)
		}
	}
}

func TestResolveMissingRoot(t *testing.T) {
	if _, err := resolve(map[string]*packageInfo{}, []string{SSHServer}); err == nil {
		t.Fatal("索引缺少根包时应返回错误")
	}
}

func TestTarEntries(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	write := func(h *tar.Header, data string) {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(data))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if data != "" {
			if _, err := tw.Write([]byte(data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(&tar.Header{Name: "./usr/", Typeflag: tar.TypeDir, Mode: 0755}, "")
	write(&tar.Header{Name: "./usr/bin/sshd", Typeflag: tar.TypeReg, Mode: 0755}, "SSHD")
	write(&tar.Header{Name: "./usr/lib/libc.so.6", Typeflag: tar.TypeReg, Mode: 0644}, "EXISTING")
	write(&tar.Header{Name: "./usr/lib/libcrypto.so.3", Typeflag: tar.TypeSymlink, Linkname: "libcrypto.so.3.5", Mode: 0777}, "")
	write(&tar.Header{Name: "./usr/lib/hard", Typeflag: tar.TypeLink, Linkname: "./usr/bin/sshd", Mode: 0755}, "")
	write(&tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0644}, "BAD")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	existing := map[string]bool{"usr": true, "usr/lib/libc.so.6": true}
	entries, err := tarEntries(&buf, existing)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, e := range entries {
		got[e.Name] = int(e.Mode)
	}
	if _, ok := got["usr/bin/sshd"]; !ok {
		t.Error("缺少 usr/bin/sshd")
	}
	if _, ok := got["usr/lib/libc.so.6"]; ok {
		t.Error("已存在的库不应被覆盖")
	}
	if mode, ok := got["usr/lib/libcrypto.so.3"]; !ok || mode&0170000 != 0120000 {
		t.Errorf("符号链接模式错误：%o", mode)
	}
	hard, ok := got["usr/lib/hard"]
	if !ok {
		t.Error("缺少硬链接")
	} else if hard&0170000 != 0100000 {
		t.Errorf("硬链接应转换为普通文件：%o", hard)
	}
	for _, e := range entries {
		if e.Name == "../escape" || strings.HasPrefix(e.Name, "/") {
			t.Errorf("不应包含越界路径 %q", e.Name)
		}
	}
}
