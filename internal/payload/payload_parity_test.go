package payload

import (
	"strings"
	"testing"
)

// TestCredentialParity 对比安装器与目标系统的登录凭据是否一致。
func TestCredentialParity(t *testing.T) {
	cases := []struct {
		name string
		user string
		keys string
	}{
		{"root/无公钥", "root", ""},
		{"deploy/无公钥", "deploy", ""},
		{"root/有公钥", "root", "ssh-ed25519 AAAA k\n"},
		{"deploy/有公钥", "deploy", "ssh-ed25519 AAAA k\n"},
	}
	for _, c := range cases {
		cfg := baseConfig()
		cfg.Username = c.user
		cfg.InstallerSSH = true
		cfg.AuthorizedKeys = c.keys

		ps, err := Preseed(cfg)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := Build(cfg)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, e := range entries {
			found[e.Name] = string(e.Data)
		}

		var psUser, psHash string
		for _, l := range strings.Split(ps, "\n") {
			switch {
			case strings.HasPrefix(l, "d-i passwd/username string "):
				psUser = strings.TrimPrefix(l, "d-i passwd/username string ")
			case strings.HasPrefix(l, "d-i passwd/root-password-crypted password "):
				psUser, psHash = "root", strings.TrimPrefix(l, "d-i passwd/root-password-crypted password ")
			case strings.HasPrefix(l, "d-i passwd/user-password-crypted password "):
				psHash = strings.TrimPrefix(l, "d-i passwd/user-password-crypted password ")
			}
		}

		var inUser string
		for _, l := range strings.Split(found["clearme-assets/installer-ssh.sh"], "\n") {
			if strings.HasPrefix(l, "USER_NAME=") {
				inUser = strings.TrimPrefix(l, "USER_NAME=")
			}
		}
		inPass := strings.TrimSpace(found["configs/clearme-installer-pass"])
		hasKeys := found["configs/clearme-installer-keys"] != ""

		t.Logf("=== %s ===", c.name)
		t.Logf("  目标系统: 用户=%-6s 密码hash=%q", psUser, psHash)
		t.Logf("  安装器:   用户=%-6s 密码文件=%q 公钥文件=%v", inUser, inPass, hasKeys)
		if psUser != inUser {
			t.Errorf("  用户不一致: 目标=%q 安装器=%q", psUser, inUser)
		}
		if psHash != inPass {
			t.Errorf("  密码不一致: 目标=%q 安装器=%q", psHash, inPass)
		}
		if hasKeys && !strings.Contains(found["clearme-assets/installer-ssh.sh"], "password_auth=no") {
			t.Errorf("  有公钥时安装器应关闭密码登录")
		}
	}
}
