package app

import (
	"fmt"

	"github.com/naipad/clearme/internal/grub"
	"github.com/naipad/clearme/internal/system"
)

// Reset 删除启动项、grubenv 中的 next_entry 和已下载的安装器文件。
func Reset() error {
	if !system.IsRoot() {
		return fmt.Errorf("需要 root 权限")
	}
	return grub.Reset()
}
