# clearme

一键重装 Debian 系统，单文件二进制。

## 安装

```bash
# 仅安装到 /usr/local/bin/clearme
curl -fsSL https://raw.githubusercontent.com/naipad/clearme/main/install.sh | sh
```

```bash
# 安装后立即执行，-- 后面的参数原样传给 clearme
curl -fsSL https://raw.githubusercontent.com/naipad/clearme/main/install.sh \
  | sudo bash -s -- \
    --yes \
    --user deploy \
    --password 'S3cret'
```

## 快速开始

```bash
# 准备重装，确认后手动 reboot
clearme --china

# 指定磁盘、账户和端口
clearme --disk /dev/sda --user deploy --password 'S3cret' --ssh-port 2222

# btrfs 根分区并开启压缩
clearme --filesystem btrfs --btrfs-compress zstd:3

# 只预览配置，不下载安装器、不改系统
clearme --dry-run

# 准备完成后立即重启
clearme --reboot-now --yes
```

`--version` 表示 **Debian 版本**，clearme 自身版本用 `-V` 查看。
随机密码会在摘要的“登录信息”里显示一次，请先保存。

安装器默认开启 SSH，安装期间可以用目标系统的账户直接登录查看进度：

```bash
ssh -p 22 用户名@服务器IP
tail -fn+1 /var/log/syslog   # 安装日志
dmesg -w                     # 内核日志
```

登录后的提示会列出这些命令。串口/网络控制台启动时，安装器运行在 screen 会话中，提示里还会给出接入安装界面的 `screen -x` 命令。

## 子命令

```bash
clearme status          # 安装文件、GRUB 启动项、下次启动目标
clearme logs            # 准备阶段日志
clearme logs firstboot  # 新系统首次启动日志（清理完成后删除）
clearme logs cleanup    # 首次启动后的清理日志（清理完成后删除）
clearme logs all        # 全部日志
clearme reset           # 删除启动项和已下载文件（重启前取消重装）
```

## 参数

| 参数 | 说明 |
| --- | --- |
| `--version VERSION` | 目标版本：11/12/13/14/sid 或代号，默认跟随 stable（当前 13） |
| `--mirror URL` | 完整镜像地址，默认 `deb.debian.org` |
| `--china` | 使用国内镜像，自动测速选择最快的一个 |
| `--proxy URL` | 下载与目标系统 apt 的代理（http/https/socks5/socks5h） |
| `--ip` / `--netmask` / `--gateway` | 指定静态 IPv4，默认沿用当前网络方式 |
| `--ipv6 ADDR[/PREFIX]` | 指定静态 IPv6，网关默认沿用当前 RA |
| `--ipv6-gateway ADDR` | IPv6 网关，需配合 `--ipv6` |
| `--ipv6-dns "ADDR ..."` | IPv6 DNS 服务器 |
| `--dns "SERVERS"` | 指定 DNS，默认沿用当前系统 |
| `--hostname NAME` | 主机名，默认沿用当前主机名 |
| `--user NAME` | 登录用户，默认 `root` |
| `--password PASS` | 登录密码，省略则随机生成 |
| `--ssh-port N` | SSH 端口，默认 22 |
| `--authorized-keys-url URL` | 写入 SSH 公钥并关闭密码登录 |
| `--disk /dev/sdX` | 目标磁盘，默认根分区所在磁盘 |
| `--filesystem FS` | 根文件系统：ext4/xfs/btrfs，默认 ext4 |
| `--btrfs-compress ALGO` | btrfs 根分区压缩：`zstd[:1-15]`、`zlib[:1-9]` 或 `lzo`，需配合 `--filesystem btrfs` |
| `--keep-data` | 保留数据分区（自动识别） |
| `--keep-part /dev/sdX3` | 指定保留分区，可重复 |
| `--install "PKG ..."` | 额外安装的软件包 |
| `--upgrade none\|safe\|full` | 安装阶段 apt 升级，默认 full |
| `--post-install PATH\|URL` | 首次启动执行的脚本 |
| `--apt-repo 'deb ...'` | 额外 apt 源，可重复 |
| `--fail2ban` | 安装 fail2ban 并启用 SSH 防爆破 |
| `--firmware` | 附带 non-free 固件 |
| `--swap-size SIZE` | 创建 swapfile，如 `512M`、`2G`、`2048MB`；默认内存小于 1 GB 时自动创建 |
| `--dry-run` | 只打印配置摘要 |
| `--yes` | 跳过确认 |
| `--reboot-now` | 准备完成后立即重启 |
| `--force` | 跳过容器/Secure Boot/内存检查 |
| `-V` / `--program-version` | 显示 clearme 版本 |

其余按默认值处理：ext4、自动 UEFI/BIOS、`Asia/Shanghai` 时区、自动安全更新、安装器 SSH 开启；swap 仅在内存小于 1 GB 时自动创建。

## 注意

- 只支持 Debian → Debian，不支持 Windows 或其他 Linux 源系统。
- 重装会清空目标磁盘，执行前先 `--dry-run` 核对，并确认有 VNC/IPMI/串口等带外控制。
- UEFI 机器需要关闭 Secure Boot。
- `--keep-data` 建议把数据分区放在磁盘末尾，并在多盘机器上先断开其他磁盘。
- 重启前想取消：`clearme reset`。
