// Package diudeb 下载 Debian 安装器的 udeb 组件，并把 initrd 中缺少的
// 文件转换成 cpio 项。目前用于在安装器环境中启动 sshd。
package diudeb

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/naipad/clearme/internal/cpio"
	"github.com/naipad/clearme/internal/initrd"
	"github.com/naipad/clearme/internal/mirror"
)

const (
	// SSHServer 是安装器里可用的 OpenSSH 服务端组件。
	SSHServer = "openssh-server-udeb"
	// Screen 只在安装器自带 screen 缺失时才补充下载。
	Screen = "screen-udeb"
)

// packageInfo 是 Packages 索引中我们关心的字段。
type packageInfo struct {
	Name     string
	Filename string
	SHA256   string
	Depends  [][]string // 每个元素是一组用 | 分隔的候选包名
}

// Inject 下载安装器 SSH 所需的 udeb，返回 initrd 中尚不存在的文件。
// 安装器自带的库(例如 libc、libcrypto)会被原样保留。
func Inject(ctx context.Context, m mirror.Mirror, suite, arch, initrdPath string, logf func(string, ...any)) ([]cpio.Entry, error) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		return nil, fmt.Errorf("找不到 dpkg-deb；安装器 SSH 需要 Debian 系统自带的 dpkg")
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}

	existing, err := initrd.Names(initrdPath)
	if err != nil {
		return nil, err
	}

	roots := []string{SSHServer}
	if !existing["usr/bin/screen"] && !existing["bin/screen"] {
		roots = append(roots, Screen)
	}

	dir, err := os.MkdirTemp("", "clearme-udeb-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	index, base, err := loadIndex(ctx, dir, m, suite, arch)
	if err != nil {
		return nil, err
	}
	order, err := resolve(index, roots)
	if err != nil {
		return nil, err
	}

	var entries []cpio.Entry
	for _, name := range order {
		pkg := index[name]
		dest := filepath.Join(dir, name+".udeb")
		logf("正在下载安装器组件 %s ...", name)
		if err := mirror.Download(ctx, base+"/"+pkg.Filename, dest); err != nil {
			return nil, fmt.Errorf("下载 %s 失败：%w", name, err)
		}
		if err := mirror.VerifyFile(dest, pkg.SHA256); err != nil {
			return nil, fmt.Errorf("%s 校验失败：%w", name, err)
		}
		files, err := debEntries(ctx, dest, existing)
		if err != nil {
			return nil, fmt.Errorf("解包 %s 失败：%w", name, err)
		}
		entries = append(entries, files...)
	}
	return entries, nil
}

// loadIndex 下载并解析 debian-installer 的 Packages 索引；优先使用所选镜像，
// 失败时退回 deb.debian.org。
func loadIndex(ctx context.Context, dir string, m mirror.Mirror, suite, arch string) (map[string]*packageInfo, string, error) {
	bases := []string{m.Base()}
	if bases[0] != "https://deb.debian.org/debian" {
		bases = append(bases, "https://deb.debian.org/debian")
	}
	var lastErr error
	for _, base := range bases {
		url := fmt.Sprintf("%s/dists/%s/main/debian-installer/binary-%s/Packages.gz", base, suite, arch)
		path := filepath.Join(dir, "Packages.gz")
		pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := mirror.Download(pctx, url, path)
		cancel()
		if err != nil {
			lastErr = err
			os.Remove(path)
			continue
		}
		index, err := parseIndexFile(path)
		if err != nil {
			lastErr = err
			continue
		}
		if len(index) == 0 {
			lastErr = fmt.Errorf("%s 中没有安装器组件", url)
			continue
		}
		return index, base, nil
	}
	return nil, "", fmt.Errorf("无法获取安装器组件索引：%w", lastErr)
}

func parseIndexFile(path string) (map[string]*packageInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("Packages.gz 不是 gzip 格式：%w", err)
	}
	defer zr.Close()
	return parseIndex(zr)
}

// parseIndex 解析 Debian control 格式的 Packages 文件，只保留包名、
// 依赖和下载位置。
func parseIndex(r io.Reader) (map[string]*packageInfo, error) {
	index := make(map[string]*packageInfo)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)

	var cur *packageInfo
	field := ""
	flush := func() {
		if cur != nil && cur.Name != "" && cur.Filename != "" {
			index[cur.Name] = cur
		}
		cur = nil
		field = ""
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if cur != nil && field == "depends" {
				cur.Depends = append(cur.Depends, parseDepends(strings.TrimSpace(line))...)
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if cur == nil {
			cur = &packageInfo{}
		}
		field = key
		switch key {
		case "package":
			cur.Name = value
		case "filename":
			cur.Filename = value
		case "sha256":
			cur.SHA256 = value
		case "depends":
			cur.Depends = append(cur.Depends, parseDepends(value)...)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	return index, nil
}

// parseDepends 把 “a (>= 1), b | c” 拆成 [[a] [b c]]。
func parseDepends(s string) [][]string {
	var groups [][]string
	for _, part := range strings.Split(s, ",") {
		var alts []string
		for _, alt := range strings.Split(part, "|") {
			name := strings.TrimSpace(alt)
			if i := strings.IndexByte(name, '('); i >= 0 {
				name = strings.TrimSpace(name[:i])
			}
			if i := strings.IndexByte(name, ':'); i >= 0 {
				name = name[:i]
			}
			if name != "" {
				alts = append(alts, name)
			}
		}
		if len(alts) > 0 {
			groups = append(groups, alts)
		}
	}
	return groups
}

// resolve 按广度优先展开依赖。根包必须存在；依赖缺失时跳过，
// 因为安装器可能通过其他组件提供同名文件。
func resolve(index map[string]*packageInfo, roots []string) ([]string, error) {
	var order []string
	seen := make(map[string]bool)
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		pkg, ok := index[name]
		if !ok {
			if len(order) == 0 {
				return nil, fmt.Errorf("镜像中没有 %s", name)
			}
			continue
		}
		seen[name] = true
		order = append(order, name)
		for _, alts := range pkg.Depends {
			for _, alt := range alts {
				if _, ok := index[alt]; ok {
					queue = append(queue, alt)
					break
				}
			}
		}
	}
	return order, nil
}

// debEntries 用 dpkg-deb 读取 udeb 的文件系统 tar，再转换成 cpio 项。
func debEntries(ctx context.Context, debPath string, existing map[string]bool) ([]cpio.Entry, error) {
	cmd := exec.CommandContext(ctx, "dpkg-deb", "--fsys-tarfile", debPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	entries, terr := tarEntries(out, existing)
	// 即使转换失败也要把管道读完，避免 dpkg-deb 阻塞在写端。
	io.Copy(io.Discard, out)
	werr := cmd.Wait()
	if terr != nil {
		return nil, terr
	}
	if werr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%v: %s", werr, msg)
		}
		return nil, werr
	}
	return entries, nil
}

// tarEntries 把 udeb 的 data.tar 转换成 cpio 项，跳过 initrd 里已存在的
// 路径，避免用镜像里的新版本覆盖安装器正在使用的库。
func tarEntries(r io.Reader, existing map[string]bool) ([]cpio.Entry, error) {
	tr := tar.NewReader(r)
	var entries []cpio.Entry
	files := make(map[string][]byte)

	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name, ok := cleanPath(h.Name)
		if !ok || existing[name] {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			entries = append(entries, cpio.Dir(name, uint32(h.Mode)&0777))
			existing[name] = true
		case tar.TypeReg:
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			entries = append(entries, cpio.File(name, uint32(h.Mode)&0777, data))
			files[name] = data
			existing[name] = true
		case tar.TypeSymlink:
			entries = append(entries, cpio.Symlink(name, h.Linkname))
			existing[name] = true
		case tar.TypeLink:
			target, ok := cleanPath(h.Linkname)
			if !ok {
				continue
			}
			if data, ok := files[target]; ok {
				entries = append(entries, cpio.File(name, uint32(h.Mode)&0777, data))
				existing[name] = true
			}
		}
	}
	return entries, nil
}

// cleanPath 去掉 tar 里的 ./ 前缀并拒绝越界路径。
func cleanPath(name string) (string, bool) {
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimSuffix(name, "/")
	if name == "" || name == "." {
		return "", false
	}
	if strings.HasPrefix(name, "/") || name == ".." ||
		strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
		return "", false
	}
	return name, true
}
