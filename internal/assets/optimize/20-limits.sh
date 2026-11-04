#!/bin/sh
# 由 clearme 生成：文件句柄与进程数限制。
set -u

cat > /etc/security/limits.d/90-nofile.conf <<'LIM'
# 普通用户
* soft nofile 65535
* hard nofile 65535
* soft nproc 65535
* hard nproc 65535
# root 用户（部分服务以 root 启动，单独声明）
root soft nofile 65535
root hard nofile 65535
root soft nproc 65535
root hard nproc 65535
LIM

# systemd 服务不经过 PAM，需要在 system.conf.d 里设置默认值。
install -d -m 0755 /etc/systemd/system.conf.d
cat > /etc/systemd/system.conf.d/90-default-limits.conf <<'SCONF'
[Manager]
DefaultLimitNOFILE=65535
DefaultLimitNPROC=65535
SCONF
systemctl daemon-reexec 2>/dev/null || true
