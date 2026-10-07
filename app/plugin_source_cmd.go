package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/plugins/source"
	"github.com/yogasw/wick/internal/userconfig"
)

// withSourceManager opens the DB and wires a source.Manager whose PAT
// encryption uses the same master key as the server.
func withSourceManager(ctx context.Context, fn func(m *source.Manager) error) error {
	userconfig.ResolveDBPath(BuildAppName, "")
	db := postgres.NewGORM(config.Load().Database)
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	cfg, err := sourceConfigs(ctx, db)
	if err != nil {
		return err
	}
	return fn(&source.Manager{DB: db, Client: source.NewClient(cfg.DecryptSecret), Encrypt: cfg.EncryptSecret})
}

func sourceConfigs(ctx context.Context, db *gorm.DB) (*configs.Service, error) {
	cfg := configs.NewService(db)
	if err := cfg.Bootstrap(ctx); err != nil {
		return nil, fmt.Errorf("configs: %w", err)
	}
	e, err := enc.New(cfg.EncryptionKey())
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	cfg.SetEncryptor(e)
	return cfg, nil
}

func pluginSourceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "source", Short: "Manage plugin sources (plugins.json URL or GitHub releases)"}
	cmd.AddCommand(pluginSourceAddCmd(), pluginSourceListCmd(), pluginSourceRemoveCmd(), pluginSourceCheckCmd())
	return cmd
}

func pluginSourceAddCmd() *cobra.Command {
	var in source.SourceInput
	var patEnv string
	cmd := &cobra.Command{
		Use:   "add <https-url-to-plugins.json|owner/repo>",
		Short: "Add a plugin source; owner/repo means GitHub releases",
		Long: `Add a plugin source.

  plugin source add https://example.com/plugins.json
  plugin source add acme/wick-plugins --private --pat-env GH_PLUGINS_PAT --pubkey <base64>

The PAT is read from an environment variable (never a flag, so it stays out of
shell history) and stored encrypted. auto-update is off unless --auto-update.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
				in.Type, in.URL = source.TypeURL, target
			} else {
				in.Type, in.Repo = source.TypeGitHub, target
			}
			if patEnv != "" {
				in.PAT = os.Getenv(patEnv)
				if in.PAT == "" {
					return fmt.Errorf("$%s is empty", patEnv)
				}
			}
			return withSourceManager(cmd.Context(), func(m *source.Manager) error {
				s, err := m.Save("", in, "cli")
				if err != nil {
					return err
				}
				fmt.Printf("added source %s (%s)\n", s.ID, s.Name)
				return nil
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Name, "name", "", "display name")
	f.BoolVar(&in.Private, "private", false, "private GitHub repo (downloads through the API with the PAT)")
	f.StringVar(&patEnv, "pat-env", "", "environment variable holding a fine-grained GitHub PAT (Contents: read)")
	f.StringVar(&in.PubKey, "pubkey", "", "pin the publisher's base64 ed25519 key (signatures become required)")
	f.StringVar(&in.KeyFilter, "keys", "", "comma-separated plugin keys to take from this source")
	f.BoolVar(&in.AllowPrerelease, "prerelease", false, "consider prereleases")
	f.BoolVar(&in.AutoUpdate, "auto-update", false, "install newer versions automatically")
	f.IntVar(&in.PollMinutes, "poll", source.DefaultPollMinutes, "poll interval in minutes")
	return cmd
}

func pluginSourceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List plugin sources",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSourceManager(cmd.Context(), func(m *source.Manager) error {
				list, err := m.List()
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tTYPE\tNAME\tPLUGINS\tAUTO-UPDATE\tLAST CHECK\tSTATUS")
				for _, s := range list {
					last := "never"
					if s.LastCheckAt != nil {
						last = s.LastCheckAt.Format(time.RFC3339)
					}
					status := s.LastStatus
					if s.LastError != "" {
						status += ": " + s.LastError
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%v\t%s\t%s\n", s.ID, s.Type, s.Name, len(source.Entries(&s)), s.AutoUpdate, last, status)
				}
				return tw.Flush()
			})
		},
	}
}

func pluginSourceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a plugin source (installed plugins stay, without updates)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSourceManager(cmd.Context(), func(m *source.Manager) error {
				if _, err := m.Get(args[0]); err != nil {
					return err
				}
				if err := m.Delete(args[0], "cli"); err != nil {
					return err
				}
				fmt.Println("removed", args[0])
				return nil
			})
		},
	}
}

func pluginSourceCheckCmd() *cobra.Command {
	var test bool
	cmd := &cobra.Command{
		Use:   "check <id>",
		Short: "Check a source for plugins and updates (--test runs the six-step health check)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSourceManager(cmd.Context(), func(m *source.Manager) error {
				if test {
					s, err := m.Get(args[0])
					if err != nil {
						return err
					}
					for _, st := range m.Client.Test(cmd.Context(), s, nil) {
						mark := map[string]string{"ok": "OK  ", "fail": "FAIL", "skip": "SKIP"}[st.Status]
						fmt.Printf("%d. %s %-15s %s\n", st.N, mark, st.Name, st.Message)
					}
					return nil
				}
				res, err := m.Check(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				for _, e := range res.Entries {
					fmt.Printf("%-24s %-10s v%s\n", e.Key, e.Kind, strings.TrimPrefix(e.Version, "v"))
				}
				if len(res.Updates) > 0 {
					fmt.Println("updates available:", strings.Join(res.Updates, ", "))
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&test, "test", false, "run the six-step health check instead")
	return cmd
}
