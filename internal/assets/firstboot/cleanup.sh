#!/bin/sh
# 由 clearme 生成：首次启动结束时清理；如果装入了更新的内核，则在下次启动时清理。
set -u

LOG=/var/log/clearme-cleanup.log
exec >>"$LOG" 2>&1
echo "=== clearme-cleanup start: $(date) ==="

# 删除升级后不再需要的旧内核、依赖和下载缓存。
apt-get autoremove --purge -y || true
apt-get clean || true

# 移除 clearme 自身的服务与残留文件。
systemctl disable clearme-cleanup.service 2>/dev/null || true
rm -f /etc/systemd/system/clearme-cleanup.service
rm -f /etc/systemd/system/multi-user.target.wants/clearme-cleanup.service
rm -rf /root/.clearme
systemctl daemon-reload 2>/dev/null || true

echo "=== clearme-cleanup done: $(date) ==="

# 清理完成后不再保留这份日志；此后脚本不再输出。
rm -f "$LOG"
