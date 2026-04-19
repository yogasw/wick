package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var AppVersion = "dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print wick version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(AppVersion)
		},
	}
}
