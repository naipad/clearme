#!/bin/sh
# 由 clearme 生成：安装器早期按磁盘标识定位目标设备，并在 keep-data
# 模式下删除除保留分区以外的所有分区。
# 内核命令行提供：
#   clearme_disk=/dev/vda
#   clearme_disk_id=<PTUUID>
#   clearme_root_partuuid=<PARTUUID>
#   clearme_keep=2,5（仅 keep-data）
set -u

cmdline=$(cat /proc/cmdline)
getarg() {
    for w in $cmdline; do
        case "$w" in
            "$1="*) printf '%s\n' "${w#*=}"; return 0 ;;
        esac
    done
    return 1
}

disk_id=$(getarg clearme_disk_id || true)
root_uuid=$(getarg clearme_root_partuuid || true)
fallback=$(getarg clearme_disk || true)
keep=$(getarg clearme_keep || true)

normalize_id() {
    tr 'A-F' 'a-f' | tr -d '{}' | sed 's/^0x//'
}

# 读取整盘的标识：优先 blkid 的 PTUUID，失败时解析 fdisk 的
# “Disk identifier”（d-i 里的 blkid 可能是 busybox 版本）。
disk_ident() {
    dev=$1
    if command -v blkid >/dev/null 2>&1; then
        id=$(blkid -s PTUUID -o value "$dev" 2>/dev/null | normalize_id)
        if [ -n "$id" ]; then
            printf '%s\n' "$id"
            return 0
        fi
    fi
    if command -v fdisk >/dev/null 2>&1; then
        id=$(fdisk -l "$dev" 2>/dev/null | grep -i 'disk identifier' | tail -n 1 |
            sed 's/.*: *//' | normalize_id)
        if [ -n "$id" ]; then
            printf '%s\n' "$id"
            return 0
        fi
    fi
    return 1
}

# 优先用根分区的 PARTUUID 反查出所在磁盘，设备名变化也不受影响。
find_disk() {
    if [ -n "$root_uuid" ] && [ -e "/dev/disk/by-partuuid/$root_uuid" ]; then
        part=$(readlink -f "/dev/disk/by-partuuid/$root_uuid" 2>/dev/null) || part=""
        if [ -n "$part" ]; then
            name=${part##*/}
            parent=$(basename "$(readlink -f "/sys/class/block/$name/.." 2>/dev/null)" 2>/dev/null)
            if [ -n "$parent" ] && [ -b "/dev/$parent" ]; then
                printf '%s\n' "/dev/$parent"
                return 0
            fi
        fi
    fi

    # 再用磁盘标识扫描整盘。
    if [ -n "$disk_id" ]; then
        for d in /sys/block/*; do
            name=${d##*/}
            case "$name" in
                loop*|sr*|ram*|nbd*|dm-*|md*) continue ;;
            esac
            id=$(disk_ident "/dev/$name" || true)
            if [ -n "$id" ] && [ "$id" = "$disk_id" ]; then
                printf '%s\n' "/dev/$name"
                return 0
            fi
        done
    fi

    # 两个标识都拿不到时才回退到设备名，避免设备名被复用后误格式化。
    if [ -z "$disk_id" ] && [ -z "$root_uuid" ] &&
        [ -n "$fallback" ] && [ -b "$fallback" ]; then
        printf '%s\n' "$fallback"
        return 0
    fi
    return 1
}

# 等 udev 建好 /dev/disk/by-partuuid 链接。
if command -v udevadm >/dev/null 2>&1; then
    udevadm settle 2>/dev/null || true
fi

disk=$(find_disk || true)
if [ -z "$disk" ]; then
    echo "partman-early: 无法按标识找到目标磁盘 (id=${disk_id:-无} partuuid=${root_uuid:-无})" >&2
    # 不留下设备名回退路径，宁可安装失败也不要格式化错误的磁盘。
    disk=/dev/clearme-disk-not-found
fi
echo "partman-early: 目标磁盘 $disk"

if command -v debconf-set >/dev/null 2>&1; then
    debconf-set partman-auto/disk "$disk"
    debconf-set grub-installer/bootdev "$disk"
fi

# 旧 ESP 内容在 ESP 未被重新格式化时会残留；在写入引导器之前清空。
postinst=/var/lib/dpkg/info/bootstrap-base.postinst
if [ -f "$postinst" ] && [ ! -f "$postinst.clearme" ]; then
    if cp -f "$postinst" "$postinst.clearme"; then
        {
            echo 'rm -rf /target/boot/efi/*'
            cat "$postinst.clearme"
        } > "$postinst"
        chmod 755 "$postinst"
    fi
fi

if [ -n "$keep" ]; then
    if [ "$disk" = "/dev/clearme-disk-not-found" ]; then
        echo "partman-early: 保留分区模式需要可用的目标磁盘，中止" >&2
        exit 1
    fi
    if ! command -v parted >/dev/null 2>&1; then
        echo "partman-early: parted 不可用，中止" >&2
        exit 1
    fi

    oldIFS=$IFS
    IFS=','
    for n in $keep; do
        [ -n "$n" ] || continue
        if [ ! -b "$disk$n" ] && [ ! -b "${disk}p$n" ]; then
            echo "partman-early: 保留分区 $n 不存在，中止" >&2
            exit 1
        fi
    done
    IFS=$oldIFS

    numbers=$(parted -s "$disk" print 2>/dev/null | awk '/^ *[0-9]+/ {print $1}' | sort -rn)
    for n in $numbers; do
        case ",$keep," in
            *",$n,"*) continue ;;
        esac
        echo "partman-early: 删除分区 $n"
        parted -s "$disk" rm "$n" || true
    done

    if command -v partprobe >/dev/null 2>&1; then
        partprobe "$disk" 2>/dev/null || true
    fi
fi

exit 0
