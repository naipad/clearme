#!/bin/sh
# 从 GitHub Releases 安装 clearme 到 /usr/local/bin。
# 用法：
#   仅安装：
#     curl -fsSL https://raw.githubusercontent.com/naipad/clearme/main/install.sh | sh
#   安装后立即执行，参数会原样传给 clearme：
#     curl -fsSL .../install.sh | sudo bash -s -- --yes --user naipad --password '...'
set -eu

REPO=naipad/clearme
PREFIX=/usr/local/bin

case "$(uname -m)" in
    x86_64 | amd64) ARCH=amd64 ;;
    aarch64 | arm64) ARCH=arm64 ;;
    *)
        echo "不支持的架构：$(uname -m)（仅提供 amd64/arm64）" >&2
        exit 1
        ;;
esac

if [ "$(uname -s)" = "Linux" ]; then
    :
else
    echo "clearme 只支持 Linux" >&2
    exit 1
fi

if [ "$(id -u)" -ne 0 ] && [ ! -w "$PREFIX" ]; then
    echo "需要 root 权限写入 $PREFIX" >&2
    exit 1
fi

BASE="https://github.com/$REPO/releases/latest/download"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM

echo "下载 $BASE/clearme-$ARCH ..."
curl -fL --retry 3 --connect-timeout 15 -o "$TMPDIR/clearme-$ARCH" "$BASE/clearme-$ARCH"
curl -fL --retry 3 --connect-timeout 15 -o "$TMPDIR/sha256sums.txt" "$BASE/sha256sums.txt"

line="$(grep -E " [*]?clearme-$ARCH\$" "$TMPDIR/sha256sums.txt" || true)"
if [ -z "$line" ]; then
    echo "sha256sums.txt 中没有 clearme-$ARCH" >&2
    exit 1
fi
(
    cd "$TMPDIR"
    printf '%s\n' "$line" | sha256sum -c -
)

install -m 0755 "$TMPDIR/clearme-$ARCH" "$PREFIX/clearme"
echo "已安装：$PREFIX/clearme"

# 带了参数就安装后立即执行；例如 sh -s -- --yes --user naipad
if [ "$#" -gt 0 ]; then
    "$PREFIX/clearme" "$@"
    exit 0
fi

"$PREFIX/clearme" -V
