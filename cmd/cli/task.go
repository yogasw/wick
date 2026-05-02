package cli

import (
	"fmt"
	"os"
"github.com/spf13/cobra"
)

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <task>",
		Short: "Run a task from wick.yml",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runTask(args[0])
		},
	}
}

func devCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dev",
		Short: "Run dev task from wick.yml",
		RunE: func(c *cobra.Command, args []string) error {
			if err := runTask("dev"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				fmt.Println("tip: make sure wick.yml has a 'dev' task")
			}
			return nil
		},
	}
}

func setupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Run setup task from wick.yml",
		RunE: func(c *cobra.Command, args []string) error {
			if err := runTask("setup"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				fmt.Println("tip: make sure wick.yml has a 'setup' task")
				return nil
			}
			return nil
		},
	}
}

func buildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Run build task from wick.yml",
		RunE: func(c *cobra.Command, args []string) error {
			if err := runTask("build"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				fmt.Println("tip: make sure wick.yml has a 'build' task")
			}
			return nil
		},
	}
}

func testCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Run test task from wick.yml",
		RunE: func(c *cobra.Command, args []string) error {
			if err := runTask("test"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				fmt.Println("tip: make sure wick.yml has a 'test' task")
			}
			return nil
		},
	}
}

func tidyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tidy",
		Short: "Run tidy task from wick.yml",
		RunE: func(c *cobra.Command, args []string) error {
			if err := runTask("tidy"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				fmt.Println("tip: make sure wick.yml has a 'tidy' task")
			}
			return nil
		},
	}
}

func generateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Run generate task from wick.yml",
		RunE: func(c *cobra.Command, args []string) error {
			if err := runTask("generate"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				fmt.Println("tip: make sure wick.yml has a 'generate' task")
			}
			return nil
		},
	}
}

func serverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Run the HTTP server (go run . server)",
		RunE: func(c *cobra.Command, args []string) error {
			return execCmd("go run . server")
		},
	}
}

func workerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run the background job worker (go run . worker)",
		RunE: func(c *cobra.Command, args []string) error {
			return execCmd("go run . worker")
		},
	}
}
