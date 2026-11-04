#!/bin/sh
# 由 clearme 生成：logrotate 全局策略。
set -u

# 按天轮转，保留 14 份，单文件超过 50M 也轮转，压缩并带日期后缀。
cat > /etc/logrotate.conf <<'LOGROTATE'
daily
rotate 14
maxsize 50M
compress
delaycompress
dateext
missingok
notifempty
include /etc/logrotate.d
LOGROTATE
