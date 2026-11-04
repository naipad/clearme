// Package release 解析目标 Debian 版本，并生成对应的安全仓库地址。
package release

import (
	"fmt"
	"strconv"
	"strings"
)

// Info 描述一个可安装的 Debian 版本。
type Info struct {
	Suite  string // 发行版代号，例如 trixie
	Daily  bool   // 是否需要使用 d-i 每日构建
	Number int    // 主版本号，例如 13；stable 由 Release 文件动态解析
}

// Resolve 把版本号、代号或别名解析为 Info。
// 空值和 stable 表示跟随当前 stable，需要联网读取 Release 文件，
// 由应用层处理，这里只解析固定的版本号和代号。
func Resolve(v string) (Info, error) {
	switch strings.ToLower(v) {
	case "11", "bullseye", "oldoldstable":
		// bullseye 仍由 deb.debian.org 提供(含安全更新)；
		// archive.debian.org 只作为镜像回退，见 mirror.Candidates。
		return Info{Suite: "bullseye", Number: 11}, nil
	case "12", "bookworm", "oldstable":
		return Info{Suite: "bookworm", Number: 12}, nil
	case "13", "trixie":
		return Info{Suite: "trixie", Number: 13}, nil
	case "14", "forky", "testing":
		return Info{Suite: "forky", Daily: true, Number: 14}, nil
	case "sid", "unstable":
		return Info{Suite: "sid", Daily: true, Number: 99}, nil
	case "10", "buster":
		return Info{}, fmt.Errorf("Debian %s 已停止支持，镜像中不再提供安装文件", v)
	default:
		return Info{}, fmt.Errorf("不支持的 Debian 版本 %q", v)
	}
}

// StableAlias 报告 v 是否表示“跟随当前 stable”(未指定版本时同样跟随)。
func StableAlias(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "stable":
		return true
	}
	return false
}

// ParseStable 从 dists/stable/Release 解析当前 stable 的代号和主版本号。
// Release 里同时有 Codename 和 Version(例如 13.7)，代号已知时以代号为准，
// 未知代号则使用 Version 的主版本号，便于新版本发布后无需重新编译。
func ParseStable(text string) (Info, bool) {
	var code string
	number := 0
	for line := range strings.SplitSeq(text, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "codename":
			code = strings.ToLower(val)
		case "version":
			major, _, _ := strings.Cut(val, ".")
			number, _ = strconv.Atoi(major)
		}
	}
	if !validCodename(code) {
		return Info{}, false
	}
	if n := Major(code); n > 0 {
		number = n
	}
	if number <= 0 {
		return Info{}, false
	}
	return Info{Suite: code, Number: number}, true
}

// validCodename 只接受纯小写字母的代号，避免把异常内容写进配置和 URL。
func validCodename(s string) bool {
	if len(s) < 3 || len(s) > 16 || s == "sid" || s == "stable" || s == "testing" {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// Display 返回用于展示的版本号，未知代号原样返回。
func Display(suite string) string {
	switch suite {
	case "bullseye":
		return "11"
	case "bookworm":
		return "12"
	case "trixie":
		return "13"
	case "forky":
		return "14"
	}
	return suite
}

// Major 返回 Debian 主版本号；testing/sid 视为比已发布版本都新。
func Major(suite string) int {
	switch suite {
	case "bullseye":
		return 11
	case "bookworm":
		return 12
	case "trixie":
		return 13
	case "forky":
		return 14
	case "sid":
		return 99
	}
	return 0
}

// SecuritySuite 返回安全仓库对应的 suite 名称。
func SecuritySuite(suite string) string {
	if suite == "buster" {
		return "buster/updates"
	}
	return suite + "-security"
}

// SecurityRepo 生成安全仓库地址；daily 构建和 sid 没有独立的安全仓库。
// ftp.debian.org 不提供 debian-security，因此改用 deb.debian.org。
func SecurityRepo(protocol, host, dir, suite string, daily bool) string {
	if daily || suite == "sid" {
		return ""
	}
	if host == "ftp.debian.org" {
		host = "deb.debian.org"
	}
	return fmt.Sprintf("%s://%s%s-security", protocol, host, strings.TrimSuffix(dir, "/"))
}
