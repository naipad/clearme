// clearme 从正在运行的 Debian 系统在线重装为干净的 Debian。
package main

import (
	"os"

	"github.com/naipad/clearme/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
