#!/bin/bash
# 首次启动时按 /etc/network/interfaces 里的 "# mac" 标记修正网卡名。
#
# 安装器里探测到的网卡名，和目标系统内核/udev 给出的名字可能不一致
# (例如安装时 ens3，启动后主名变成 enp0s3)，ifup 找不到设备后网卡一直
# 保持 DOWN，系统装好却没有网络。本脚本在 networking.service 之前运行，
# 按 MAC 找回真实设备名并改写 interfaces。
set -eu

IFACES=/etc/network/interfaces
SYSFS=/sys/class/net

[ -f "$IFACES" ] || exit 0

log() { echo "fix-eth-name: $*" >&2; }

# 当前所有网卡名（不含 lo），已排序，便于逐秒比较。
list_devs() {
    ls "$SYSFS" 2>/dev/null | grep -vx 'lo' || true
}

# udev 可能改名多次，连续 10 秒没有变化才认为名字稳定。
old=
stable=0
waited=0
while :; do
    cur=$(list_devs)
    if [ -n "$cur" ] && [ "$old" = "$cur" ]; then
        stable=$((stable + 1))
    else
        stable=0
    fi
    old=$cur
    if [ -n "$cur" ] && [ "$stable" -ge 10 ]; then
        break
    fi
    # 60 秒内一张网卡都没出现，说明驱动缺失，交给上层报错。
    if [ -z "$cur" ] && [ "$waited" -ge 60 ]; then
        log "60 秒内未出现任何网卡，放弃"
        exit 1
    fi
    waited=$((waited + 1))
    sleep 1
done

# 按 MAC 查找当前设备名；带 master 的从设备(如 Azure 加速网络 VF)不参与匹配。
get_ethx_by_mac() {
    want=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
    [ -n "$want" ] || return 1
    for dev in $(list_devs); do
        if [ -e "$SYSFS/$dev/master" ]; then
            continue
        fi
        if [ ! -r "$SYSFS/$dev/address" ]; then
            continue
        fi
        addr=$(tr '[:upper:]' '[:lower:]' <"$SYSFS/$dev/address")
        if [ "$addr" = "$want" ]; then
            printf '%s' "$dev"
            return 0
        fi
    done
    return 1
}

tmp=$IFACES.clearme-tmp
rm -f "$tmp"

ethx=
while IFS= read -r line || [ -n "$line" ]; do
    keep=1
    case "$line" in
    '# mac '*)
        mac=${line#'# mac '}
        ethx=$(get_ethx_by_mac "$mac" || true)
        if [ -z "$ethx" ]; then
            # MAC 对不上时，若只有一张网卡就直接用它，避免装完直接断网。
            if [ "$(list_devs | wc -l)" -eq 1 ]; then
                ethx=$(list_devs)
                log "MAC $mac 未匹配，按唯一网卡处理：$ethx"
            else
                log "MAC $mac 未匹配，保留原配置"
            fi
        else
            log "MAC $mac -> $ethx"
        fi
        # 标记行用完即删。
        keep=0
        ;;
    esac
    if [ "$keep" -eq 1 ] && [ -n "$ethx" ]; then
        case "$line" in
        'iface '* | 'auto '* | 'allow-hotplug '*)
            oldname=$(printf '%s' "$line" | awk '{print $2}')
            if [ -n "$oldname" ] && [ "$oldname" != "lo" ] && [ "$oldname" != "$ethx" ]; then
                line=$(printf '%s' "$line" | awk -v d="$ethx" '{$2 = d; print}')
            fi
            ;;
        *' dev '*)
            line=$(printf '%s' "$line" | sed -E "s/[^ ]+$/$ethx/")
            ;;
        esac
    fi
    if [ "$keep" -eq 1 ]; then
        printf '%s\n' "$line" >>"$tmp"
    fi
done <"$IFACES"

# 用 cat 覆盖而不是 mv，保留原文件的权限和 inode。
if [ -s "$tmp" ]; then
    cat "$tmp" >"$IFACES"
fi
rm -f "$tmp"
