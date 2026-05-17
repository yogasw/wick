package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/internal/agents/workflow/wftest"
)

func workflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Workflow utilities",
	}
	cmd.AddCommand(workflowTestCmd())
	return cmd
}

func workflowTestCmd() *cobra.Command {
	var filter string

	cmd := &cobra.Command{
		Use:   "test <id>",
		Short: "Run __tests__/ fixtures for a workflow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			layout := agentconfig.NewLayout(agentconfig.ResolveBaseDir(agentconfig.WorkspaceConfig{}))
			svc := service.New(layout)
			eng := engine.New(layout, svc, nil)
			runner := wftest.New(eng, svc, layout)

			cases, err := runner.LoadCases(id)
			if err != nil {
				return fmt.Errorf("load cases: %w", err)
			}
			if len(cases) == 0 {
				fmt.Fprintf(os.Stderr, "no test cases found in %s/__tests__/\n", id)
				return nil
			}

			results, err := runner.RunAll(context.Background(), id)
			if err != nil {
				return fmt.Errorf("run: %w", err)
			}

			pass, fail := 0, 0
			for _, r := range results {
				if filter != "" && r.Name != filter {
					continue
				}
				if r.Pass {
					pass++
					fmt.Printf("  ✓ %s (%dms)\n", r.Name, r.Duration.Milliseconds())
				} else {
					fail++
					fmt.Printf("  ✗ %s (%dms)\n", r.Name, r.Duration.Milliseconds())
					for _, f := range r.Failures {
						fmt.Printf("      %s\n", f)
					}
				}
			}
			fmt.Printf("\n%d passed, %d failed\n", pass, fail)
			if fail > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&filter, "filter", "", "run only the test case with this name")
	return cmd
}
