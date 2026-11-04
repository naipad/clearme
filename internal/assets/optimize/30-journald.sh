#!/bin/sh
# 由 clearme 生成：journald 日志策略。
set -u

# 限制日志占用：总量 100M，运行时 30M，最长保留 7 天。
cat > /etc/systemd/journald.conf <<'JOUR'
[Journal]
Storage=auto
Compress=yes
ForwardToSyslog=no
SystemMaxUse=100M
RuntimeMaxUse=30M
MaxRetentionSec=7day
RateLimitIntervalSec=30s
RateLimitBurst=500
JOUR
systemctl restart systemd-journald 2>/dev/null || true
journalctl --vacuum-size=100M >/dev/null 2>&1 || true
journalctl --vacuum-time=7d >/dev/null 2>&1 || true
