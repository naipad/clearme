package initrd

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Names 读取 initrd 中已有的文件路径(不含 ./ 前缀)，用于决定
// 追加组件时需要补齐哪些文件，避免覆盖安装器自带的库。
func Names(gzipPath string) (map[string]bool, error) {
	f, err := os.Open(gzipPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("initrd 不是 gzip 格式：%w", err)
	}
	defer zr.Close()

	names := make(map[string]bool)
	br := bufio.NewReader(zr)
	for {
		hdr, err := readHeader(br)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}
		size, err := parseHex(hdr[54:62])
		if err != nil {
			return nil, err
		}
		nameSize, err := parseHex(hdr[94:102])
		if err != nil {
			return nil, err
		}
		raw := make([]byte, nameSize)
		if _, err := io.ReadFull(br, raw); err != nil {
			return nil, fmt.Errorf("读取 cpio 文件名失败：%w", err)
		}
		if _, err := io.CopyN(io.Discard, br, int64(padding(110+nameSize))); err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(string(raw), "\x00")
		if _, err := io.CopyN(io.Discard, br, int64(size+padding(size))); err != nil {
			return nil, err
		}
		// TRAILER 之后可能还有 clearme 追加的归档，继续读取。
		if name == "TRAILER!!!" {
			continue
		}
		name = strings.TrimPrefix(strings.TrimSuffix(name, "/"), "./")
		if name != "" && name != "." {
			names[name] = true
		}
	}
	return names, nil
}

// readHeader 读取一个 cpio 头，跳过归档之间可能存在的 0 填充。
func readHeader(br *bufio.Reader) ([]byte, error) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == 0 {
			continue
		}
		hdr := make([]byte, 110)
		hdr[0] = b
		if _, err := io.ReadFull(br, hdr[1:]); err != nil {
			return nil, err
		}
		magic := string(hdr[:6])
		if magic == "070701" || magic == "070702" {
			return hdr, nil
		}
		return nil, fmt.Errorf("initrd 中出现无法识别的 cpio 头")
	}
}

func parseHex(b []byte) (int, error) {
	v, err := strconv.ParseUint(string(b), 16, 32)
	if err != nil {
		return 0, fmt.Errorf("initrd 的 cpio 头字段无效：%q", string(b))
	}
	return int(v), nil
}

func padding(n int) int {
	if r := n % 4; r != 0 {
		return 4 - r
	}
	return 0
}
