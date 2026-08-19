package cmd

import (
	"github.com/hardcore-os/plato/domain/message"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(messageCmd)
}

var messageCmd = &cobra.Command{
	Use: "message",
	Run: MessageHandle,
}

func MessageHandle(cmd *cobra.Command, args []string) {
	message.RunMain(ConfigPath)
}
