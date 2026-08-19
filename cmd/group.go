package cmd

import (
	"github.com/hardcore-os/plato/domain/group"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(groupCmd)
}

var groupCmd = &cobra.Command{
	Use: "group",
	Run: GroupHandle,
}

func GroupHandle(cmd *cobra.Command, args []string) {
	group.RunMain(ConfigPath)
}
