package cmd

import (
	"github.com/hardcore-os/plato/domain/friend"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(friendCmd)
}

var friendCmd = &cobra.Command{
	Use: "friend",
	Run: FriendHandle,
}

func FriendHandle(cmd *cobra.Command, args []string) {
	friend.RunMain(ConfigPath)
}
