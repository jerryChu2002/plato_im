//go:build guiclient

// 这个子命令只在编译带 -tags guiclient 时才会被包含进二进制(见Dockerfile.guiclient)。
// 原因：guiclient包依赖Fyne(CGO+OpenGL+X11)，如果让gateway/state/message/friend/group
// 共用的主Dockerfile编译出的那个单一plato二进制也链接进这些依赖，会强迫所有服务的
// 构建环境都要装CGO+X11开发库，体积和构建复杂度都被不必要地拖累。
package cmd

import (
	"github.com/hardcore-os/plato/guiclient"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(guiClientCmd)
}

var guiClientCmd = &cobra.Command{
	Use: "guiclient",
	Run: GuiClientHandle,
}

func GuiClientHandle(cmd *cobra.Command, args []string) {
	guiclient.RunMain(ConfigPath)
}
