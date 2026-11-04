#!/bin/sh
# 由 clearme 生成：安装器早期的统一入口。
# 1) 启动安装器 SSH(只有启用时才会注入 installer-ssh.sh)；
# 2) 把安全仓库钩子交给 base-installer，在内核选择前生效。
set -u

if [ -x /clearme-assets/installer-ssh.sh ]; then
    sh /clearme-assets/installer-ssh.sh || true
fi

if [ -f /clearme-assets/post-base-installer.sh ]; then
    mkdir -p /usr/lib/post-base-installer.d
    cp /clearme-assets/post-base-installer.sh /usr/lib/post-base-installer.d/clearme-apt
    chmod 0755 /usr/lib/post-base-installer.d/clearme-apt
fi

exit 0
