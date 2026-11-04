// Package mirror 负责选择 Debian 镜像并下载安装器文件。
package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/naipad/clearme/internal/humanize"
	"github.com/naipad/clearme/internal/ui"
)

// Mirror 描述一个 Debian 归档镜像。
type Mirror struct {
	Protocol  string
	Host      string
	Directory string // 例如 /debian
}

// Base 返回镜像的根 URL。
func (m Mirror) Base() string {
	dir := strings.TrimSuffix(m.Directory, "/")
	return fmt.Sprintf("%s://%s%s", m.Protocol, m.Host, dir)
}

// NetbootDir 返回指定 suite 和架构的安装器目录。
func (m Mirror) NetbootDir(suite, arch string) string {
	return fmt.Sprintf("%s/dists/%s/main/installer-%s/current/images/netboot/debian-installer/%s",
		m.Base(), suite, arch, arch)
}

// DailyNetbootDir 返回 testing/sid 使用的每日构建安装器目录。
func DailyNetbootDir(arch string) string {
	return fmt.Sprintf("https://d-i.debian.org/daily-images/%s/daily/netboot/debian-installer/%s", arch, arch)
}

// ChinaMirrors 是 --china 时参与测速的国内镜像。
// 顺序只作兜底：测速全部失败时按这个顺序尝试。
var ChinaMirrors = []Mirror{
	{"https", "mirrors.aliyun.com", "/debian"},
	{"https", "mirrors.tuna.tsinghua.edu.cn", "/debian"},
	{"https", "mirrors.ustc.edu.cn", "/debian"},
	{"https", "mirrors.cloud.tencent.com", "/debian"},
	{"https", "repo.huaweicloud.com", "/debian"},
}

// ReleaseURL 返回指定 suite 的 Release 文件地址，用于测速和可用性检查。
func (m Mirror) ReleaseURL(suite string) string {
	return fmt.Sprintf("%s/dists/%s/Release", m.Base(), suite)
}

// ByLatency 并发探测候选镜像的 Release 文件，返回可用的镜像，
// 按响应时间从快到慢排列；全部不可用时返回空切片。
func ByLatency(ctx context.Context, cands []Mirror, suite string) []Mirror {
	type probe struct {
		m    Mirror
		ok   bool
		took time.Duration
	}
	ch := make(chan probe, len(cands))
	for _, m := range cands {
		go func(m Mirror) {
			start := time.Now()
			pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			ok := Ping(pctx, m.ReleaseURL(suite))
			ch <- probe{m, ok, time.Since(start)}
		}(m)
	}

	var reachable []probe
	for range cands {
		if p := <-ch; p.ok {
			reachable = append(reachable, p)
		}
	}
	sort.Slice(reachable, func(i, j int) bool { return reachable[i].took < reachable[j].took })

	out := make([]Mirror, len(reachable))
	for i, p := range reachable {
		out[i] = p.m
	}
	return out
}

// Candidates 生成按优先级排序的候选镜像列表。
func Candidates(user *Mirror) []Mirror {
	var out []Mirror
	if user != nil && user.Host != "" {
		out = append(out, *user)
	}
	if user == nil || user.Host == "" {
		out = append(out,
			Mirror{"https", "deb.debian.org", "/debian"},
			Mirror{"https", "cdn-aws.deb.debian.org", "/debian"},
			Mirror{"https", "ftp.debian.org", "/debian"},
		)
	} else {
		// 用户指定镜像优先，其后是两个官方回退镜像。
		out = append(out,
			Mirror{"https", "deb.debian.org", "/debian"},
			Mirror{"https", "cdn-aws.deb.debian.org", "/debian"},
		)
	}
	// 旧版本最终会从主镜像移除，archive 会一直保留。
	out = append(out, Mirror{"https", "archive.debian.org", "/debian"})
	return out
}

// client 默认使用环境变量中的代理；--proxy 会通过 SetProxy 替换它。
// 不设置全局超时：下载受调用方 context 约束
// (安装器文件 30 分钟，镜像探测 2 分钟)。
var client = newClient("")

func newClient(proxy string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil && u.Host != "" {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: transport}
}

// SetProxy 配置后续下载使用的 HTTP 代理。空字符串表示沿用环境变量。
func SetProxy(raw string) error {
	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("代理地址无效：%w", err)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("代理地址只支持 http/https/socks5/socks5h，got %q", u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("代理地址缺少主机名：%q", raw)
		}
	}
	client = newClient(raw)
	return nil
}

// Ping 检查 url 处的文件是否存在。
func Ping(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent
}

// Select 返回第一个能访问安装器文件的候选镜像。
func Select(ctx context.Context, cands []Mirror, suite, arch string) (Mirror, error) {
	var last string
	for _, m := range cands {
		base := m.NetbootDir(suite, arch)
		pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		ok := Ping(pctx, base+"/linux")
		cancel()
		if ok {
			return m, nil
		}
		last = base
	}
	return Mirror{}, fmt.Errorf("没有可用的 Debian 镜像(最后尝试：%s)", last)
}

// Download 把 url 下载到 dest，失败时重试几次；已存在的 .part 文件会断点续传。
func Download(ctx context.Context, url, dest string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 3 * time.Second):
			}
		}
		if err := downloadOnce(ctx, url, dest); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func downloadOnce(ctx context.Context, url, dest string) error {
	tmp := dest + ".part"
	var offset int64
	if st, err := os.Stat(tmp); err == nil && st.Size() > 0 {
		offset = st.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	appendMode := false
	switch resp.StatusCode {
	case http.StatusOK:
		// 服务器忽略了 Range：从头开始。
		offset = 0
	case http.StatusPartialContent:
		if offset == 0 {
			return fmt.Errorf("GET %s: 服务器返回了意外的 206 响应", url)
		}
		appendMode = true
	case http.StatusRequestedRangeNotSatisfiable:
		// 续传位置已经越界(通常说明 .part 已经完整)，删掉后重试。
		os.Remove(tmp)
		return fmt.Errorf("GET %s: 续传位置 %d 无效", url, offset)
	default:
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(tmp, flags, 0o644)
	if err != nil {
		return err
	}

	total := resp.ContentLength
	if total >= 0 {
		total += offset
	}
	body := io.Reader(resp.Body)
	if ui.IsTerminal() {
		body = &progressReader{
			r:     resp.Body,
			label: path.Base(url),
			total: total,
			done:  offset,
		}
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		if pr, ok := body.(*progressReader); ok {
			pr.finish()
		}
		// 保留 .part，下一次调用可以继续续传。
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if pr, ok := body.(*progressReader); ok {
		pr.finish()
	}
	return os.Rename(tmp, dest)
}

// progressReader 在终端里显示下载进度；非终端环境不会创建它。
type progressReader struct {
	r     io.Reader
	label string
	total int64
	done  int64
	last  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if time.Since(p.last) >= 200*time.Millisecond {
		p.last = time.Now()
		p.print()
	}
	return n, err
}

func (p *progressReader) print() {
	if p.total > 0 {
		pct := float64(p.done) * 100 / float64(p.total)
		fmt.Fprintf(ui.Stderr(), "\r  %-18s %5.1f%%  %s / %s",
			p.label, pct, humanize.Bytes(p.done), humanize.Bytes(p.total))
		return
	}
	fmt.Fprintf(ui.Stderr(), "\r  %-18s %s", p.label, humanize.Bytes(p.done))
}

func (p *progressReader) finish() {
	if p.done == 0 && p.total <= 0 {
		return
	}
	p.print()
	fmt.Fprintln(ui.Stderr())
}

// FetchText 下载 url 并返回文本内容，超过 limit 字节时报错。
// 用于 authorized_keys、SHA256SUMS 这类小文件，避免落地临时文件。
func FetchText(ctx context.Context, url string, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > limit {
		return "", fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return string(data), nil
}

// ChecksumCandidates 返回 base 所在的安装器目录中 SHA256SUMS 可能的位置。
// Debian 的 netboot 目录本身没有 SHA256SUMS；校验和放在上层的 images/ 或
// daily/ 目录(例如 .../images/SHA256SUMS 和 .../daily/SHA256SUMS)。
func ChecksumCandidates(base string) []string {
	base = strings.TrimSuffix(base, "/")
	var urls []string
	if i := strings.Index(base, "/netboot/"); i > 0 {
		urls = append(urls, base[:i]+"/SHA256SUMS")
	}
	urls = append(urls, base+"/SHA256SUMS")
	return urls
}

// FetchChecksums 下载并解析安装器目录对应的 SHA256SUMS。
// 返回值同时包含完整相对路径和文件名两种键，便于按 base 下载的文件查找。
func FetchChecksums(ctx context.Context, base string) (map[string]string, error) {
	var lastErr error
	for _, u := range ChecksumCandidates(base) {
		text, err := FetchText(ctx, u, 1<<20)
		if err != nil {
			lastErr = err
			continue
		}
		if sums := ParseChecksums(text); len(sums) > 0 {
			return selectNetbootSums(sums, netbootPrefix(base)), nil
		}
		lastErr = fmt.Errorf("%s 中没有可用的校验和", u)
	}
	return nil, lastErr
}

// ParseChecksums 解析 sha256sum 输出格式(允许文件名前的 ./ 和 * 标记)。
func ParseChecksums(text string) map[string]string {
	sums := make(map[string]string)
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		name = strings.TrimPrefix(name, "./")
		sums[name] = strings.ToLower(fields[0])
	}
	return sums
}

// isNetboot 判断校验和条目是否位于 netboot 目录(键可能不带开头的 /)。
func isNetboot(name string) bool {
	return strings.Contains("/"+name, "/netboot/")
}

// netbootPrefix 从安装器目录 URL 中取出相对 netboot 根的前缀，
// 例如 "netboot/debian-installer/amd64"。
func netbootPrefix(base string) string {
	base = strings.TrimSuffix(base, "/")
	i := strings.Index(base, "/netboot/")
	if i < 0 {
		return ""
	}
	return strings.TrimPrefix(base[i:], "/")
}

// selectNetbootSums 从安装器发布目录的校验和里挑出与当前 base 对应的条目，
// 并为每个文件补充唯一的文件名键。
//
// 安装器发布目录的 SHA256SUMS 同时包含 debian-installer/ 和 gtk/ 两套
// netboot，每日构建还包含 cdrom/、hd-media/；直接按文件名匹配会取错，
// 所以先按 base 对应的 netboot 相对路径精确过滤。
func selectNetbootSums(sums map[string]string, prefix string) map[string]string {
	filtered := make(map[string]string, len(sums))
	if prefix != "" {
		for name, sum := range sums {
			if strings.HasPrefix(name, prefix+"/") {
				filtered[name] = sum
			}
		}
	}
	if len(filtered) == 0 {
		hasNetboot := false
		for name := range sums {
			if isNetboot(name) {
				hasNetboot = true
				break
			}
		}
		for name, sum := range sums {
			if hasNetboot && !isNetboot(name) {
				continue
			}
			filtered[name] = sum
		}
	}
	out := filtered

	counts := make(map[string]int)
	first := make(map[string]string)
	for name := range out {
		short := path.Base(name)
		counts[short]++
		if counts[short] == 1 {
			first[short] = out[name]
		}
	}
	for short, n := range counts {
		if n == 1 {
			if _, exists := out[short]; !exists {
				out[short] = first[short]
			}
		}
	}
	return out
}

// VerifyFile 校验文件的 SHA-256；want 为空时直接返回 nil。
func VerifyFile(path, want string) error {
	if want == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%s 的 SHA-256 不匹配(期望 %s，实际 %s)", path, want, got)
	}
	return nil
}
