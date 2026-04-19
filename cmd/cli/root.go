package cli

import (
	"embed"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func Execute(tpl, skills embed.FS) {
	root := &cobra.Command{
		Use:   "wick",
		Short: "Scaffold agent-first Go projects",
	}
	root.AddCommand(initCmd(tpl, skills))
	root.AddCommand(devCmd())
	root.AddCommand(setupCmd())
	root.AddCommand(buildCmd())
	root.AddCommand(testCmd())
	root.AddCommand(tidyCmd())
	root.AddCommand(generateCmd())
	root.AddCommand(runCmd())
	root.AddCommand(versionCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
