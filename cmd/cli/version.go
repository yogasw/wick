package cli

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var AppVersion = "dev"

func versionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print wick version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(AppVersion)
		},
	}
	cmd.AddCommand(versionNextCmd())
	return cmd
}

// versionNextCmd reads `version:` from wick.yml, bumps the last numeric
// segment by one, writes the new value back in place (preserving any
// surrounding quotes/comments on the line), and prints the bumped value
// to stdout.
//
// Format follows whatever is already there:
//
//	1     → 2
//	0.1   → 0.2
//	0.6.4 → 0.6.5
//
// Used by release.yml in AUTO_VERSION mode: prepare job runs it to
// resolve the next tag, release job runs it again on the same baseline
// to bump wick.yml + commit back after release succeeds.
func versionNextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "next",
		Short: "Bump wick.yml version: last segment +1, write back, print new value",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile("wick.yml")
			if err != nil {
				return fmt.Errorf("read wick.yml: %w", err)
			}
			re := regexp.MustCompile(`(?m)^(version:[ \t]*["']?)([0-9]+(?:\.[0-9]+)*)`)
			m := re.FindSubmatch(data)
			if m == nil {
				return fmt.Errorf("no `version:` field with numeric value found in wick.yml")
			}
			next, err := bumpLastSegment(string(m[2]))
			if err != nil {
				return err
			}
			replaced := re.ReplaceAll(data, []byte("${1}"+next))
			if err := os.WriteFile("wick.yml", replaced, 0o644); err != nil {
				return fmt.Errorf("write wick.yml: %w", err)
			}
			fmt.Println(next)
			return nil
		},
	}
}

func bumpLastSegment(v string) (string, error) {
	segs := strings.Split(v, ".")
	last := segs[len(segs)-1]
	n, err := strconv.Atoi(last)
	if err != nil {
		return "", fmt.Errorf("last segment %q is not numeric", last)
	}
	segs[len(segs)-1] = strconv.Itoa(n + 1)
	return strings.Join(segs, "."), nil
}
