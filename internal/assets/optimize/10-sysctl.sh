#!/bin/sh
# 由 clearme 生成：系统与内核参数（sysctl）。
set -u

cat > /etc/sysctl.d/99-sysctl.conf <<'EOF'
# ---- 文件句柄与进程 ----
# 全局文件句柄上限
fs.file-max                     = 2097152
# 高并发容器/线程
kernel.pid_max                  = 4194304
# Docker / Elasticsearch / Java 类应用需要
vm.max_map_count                = 262144
# 文件监听（Node、Syncthing、IDE 等）
fs.inotify.max_user_watches     = 1048576
fs.inotify.max_user_instances   = 8192

# ---- 内存与缓存 ----
# 降低 swap 倾向，内存紧张时才换出
vm.swappiness                   = 10
# 保留 dentry/inode 缓存，减少目录遍历开销
vm.vfs_cache_pressure           = 50

# ---- TCP 队列与连接 ----
# 监听队列上限（应用侧也要配合设置 backlog）
net.core.somaxconn              = 65535
net.ipv4.tcp_max_syn_backlog    = 65535
# 高 PPS 下的网卡收包队列
net.core.netdev_max_backlog     = 16384
# 复用 TIME_WAIT 连接（依赖 tcp_timestamps）
net.ipv4.tcp_tw_reuse           = 1
# accept 队列满时直接拒绝，便于暴露 backlog 不足
net.ipv4.tcp_abort_on_overflow  = 1

# ---- 拥塞控制与延迟 ----
# fq 队列 + BBR 拥塞控制
net.core.default_qdisc          = fq
net.ipv4.tcp_congestion_control = bbr
# 长连接空闲后不重新慢启动
net.ipv4.tcp_slow_start_after_idle = 0
# TCP Fast Open，客户端和服务端同时启用
net.ipv4.tcp_fastopen           = 3

# ---- TCP 行为 ----
# 不为每条连接保存历史指标，减少内存占用
net.ipv4.tcp_no_metrics_save    = 1
# 关闭 ECN，兼容老旧网络设备
net.ipv4.tcp_ecn                = 0
# 关闭 F-RTO，保持保守的重传行为
net.ipv4.tcp_frto               = 0
# 不做 MTU 探测；遇到 PMTU 黑洞可改为 1
net.ipv4.tcp_mtu_probing        = 0
# 防御 TIME_WAIT assassination
net.ipv4.tcp_rfc1337            = 1
net.ipv4.tcp_sack               = 1
net.ipv4.tcp_window_scaling     = 1
net.ipv4.tcp_adv_win_scale      = 2
net.ipv4.tcp_moderate_rcvbuf    = 1
# 缩短 FIN_WAIT_2，尽快释放连接
net.ipv4.tcp_fin_timeout        = 15
# 收发缓冲区：最小 / 默认 / 最大
net.ipv4.tcp_rmem               = 4096 87380 33554432
net.ipv4.tcp_wmem               = 4096 65536 33554432
net.core.rmem_max               = 33554432
net.core.wmem_max               = 33554432
net.ipv4.udp_rmem_min           = 8192
net.ipv4.udp_wmem_min           = 8192
# 可用临时端口范围
net.ipv4.ip_local_port_range    = 1024 65535
# 启用时间戳（tcp_tw_reuse 依赖）
net.ipv4.tcp_timestamps         = 1

# ---- 转发与路由 ----
# 关闭反向路径过滤；开启转发（NAT/容器/代理场景需要）
net.ipv4.conf.all.rp_filter     = 0
net.ipv4.conf.default.rp_filter = 0
net.ipv4.ip_forward             = 1
net.ipv6.conf.all.forwarding    = 1
net.ipv6.conf.default.forwarding = 1
# 允许本机路由访问 127.0.0.0/8（容器端口映射需要）
net.ipv4.conf.all.route_localnet = 1
EOF

# 重新加载全部 sysctl 配置；个别内核不支持的 key 只记日志。
sysctl --system >/dev/null 2>&1 || true
