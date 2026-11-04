// Package cli 实现 clearme 的命令行入口、参数解析和帮助信息。
package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/naipad/clearme/internal/app"
	"github.com/naipad/clearme/internal/ui"
	"github.com/naipad/clearme/internal/version"
)

// Main 解析参数并执行对应操作，返回进程退出码。
func Main(args []string) int {
	// 子命令不经过常规参数解析。
	if len(args) > 0 {
		switch args[0] {
		case "reset":
			if err := app.Reset(); err != nil {
				ui.Errorf("错误: %v", err)
				return 1
			}
			ui.Successf("已移除 clearme 启动项；重启后将回到当前系统")
			return 0
		case "status":
			if err := app.Status(); err != nil {
				ui.Errorf("错误: %v", err)
				return 1
			}
			return 0
		case "logs":
			kind := ""
			if len(args) > 1 {
				kind = args[1]
			}
			if err := app.Logs(kind); err != nil {
				ui.Errorf("错误: %v", err)
				return 1
			}
			return 0
		case "help":
			Usage()
			return 0
		}
	}

	o, err := Parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			Usage()
			return 0
		}
		ui.Errorf("错误: %v", err)
		fmt.Fprintln(ui.Stderr())
		Usage()
		return 2
	}

	if o.ProgramVersion {
		fmt.Fprintf(ui.Stdout(), "clearme %s\n", version.Version)
		return 0
	}

	if o.DryRun {
		if err := app.DryRun(o); err != nil {
			ui.Errorf("错误: %v", err)
			return 1
		}
		return 0
	}

	if err := app.Run(o); err != nil {
		fmt.Fprintln(ui.Stderr())
		ui.Errorf("错误: %v", err)
		return 1
	}
	return 0
}
