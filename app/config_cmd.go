package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/lan"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/userconfig"
	"github.com/yogasw/wick/pkg/entity"
)

// withConfigsService opens the DB, boots a configs.Service (no HTTP,
// no enc), runs fn, and closes the DB. Used by every `<app> config`
// subcommand so they share one short-lived service. Concurrent with a
// running server is safe — sqlite WAL mode + busy_timeout handle the
// overlap; postgres is naturally concurrent.
func withConfigsService(fn func(ctx context.Context, svc *configs.Service) error) error {
	userconfig.ResolveDBPath(BuildAppName, "")
	db := postgres.NewGORM(config.Load().Database)
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	svc := configs.NewService(db)
	ctx := context.Background()
	if err := svc.Bootstrap(ctx); err != nil {
		return fmt.Errorf("configs bootstrap: %w", err)
	}
	return fn(ctx, svc)
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage app-level runtime configuration (app_url, allowed_origins, etc.)",
	}
	cmd.AddCommand(
		configListCmd(),
		configGetCmd(),
		configSetCmd(),
		configProfileCmd(),
		configAllowedOriginsCmd(),
	)
	return cmd
}

func configListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every app-level config row (secrets masked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				rows := svc.ListOwned("")
				sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
				for _, r := range rows {
					val := r.Value
					if r.IsSecret && val != "" {
						val = "********"
					}
					if r.EnvOverride != "" {
						fmt.Printf("%-24s = %s  (overridden by %s)\n", r.Key, val, r.EnvOverride)
					} else {
						fmt.Printf("%-24s = %s\n", r.Key, val)
					}
				}
				return nil
			})
		},
	}
}

func configGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print one config value (secrets are NOT masked — handle with care)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				v := svc.Get(args[0])
				fmt.Println(v)
				return nil
			})
		},
	}
}

func configSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Update one config value. Rejects locked rows and rows currently overridden by env.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				if err := svc.Set(ctx, args[0], args[1]); err != nil {
					return err
				}
				fmt.Printf("%s = %s\n", args[0], args[1])
				return nil
			})
		},
	}
}

func parseProfileArg(p string) (string, error) {
	switch p {
	case connectors.ProfileFull, connectors.ProfileAgent, connectors.ProfileLite:
		return p, nil
	default:
		return "", fmt.Errorf("invalid profile %q (want %s|%s|%s)",
			p, connectors.ProfileFull, connectors.ProfileAgent, connectors.ProfileLite)
	}
}

func configProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profile <full|agent|lite>",
		Short: "Set the connector profile this instance registers at boot. Takes effect on restart.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := parseProfileArg(args[0])
			if err != nil {
				return err
			}
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				if err := svc.Set(ctx, configs.KeyProfile, p); err != nil {
					return err
				}
				fmt.Printf("profile = %s (restart to apply)\n", p)
				return nil
			})
		},
	}
}

func configAllowedOriginsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "allowed-origins",
		Short: "Manage the host allowlist (URLs that may reach the admin/manager beyond app_url)",
	}
	cmd.AddCommand(
		allowedOriginsListCmd(),
		allowedOriginsAddCmd(),
		allowedOriginsRemoveCmd(),
		allowedOriginsAutodetectCmd(),
	)
	return cmd
}

func allowedOriginsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List current allowed_origins entries (one per line)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				for _, u := range svc.AllowedOrigins() {
					fmt.Println(u)
				}
				return nil
			})
		},
	}
}

func allowedOriginsAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <url>",
		Short: "Append one URL (or bare host:port) to allowed_origins. Idempotent.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				added, _, err := addAllowedOrigins(ctx, svc, args)
				if err != nil {
					return err
				}
				for _, u := range added {
					fmt.Printf("+ %s\n", u)
				}
				return nil
			})
		},
	}
}

func allowedOriginsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <url>",
		Aliases: []string{"rm"},
		Short:   "Remove one URL from allowed_origins. No-op when the URL isn't present.",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				removed, err := removeAllowedOrigins(ctx, svc, args)
				if err != nil {
					return err
				}
				for _, u := range removed {
					fmt.Printf("- %s\n", u)
				}
				return nil
			})
		},
	}
}

func allowedOriginsAutodetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "autodetect",
		Short: "Discover LAN URLs and interactively whitelist the ones you pick.",
		RunE: func(cmd *cobra.Command, args []string) error {
			port := config.Load().App.Port
			if port == 0 {
				port = 9425
			}
			ips := lan.DiscoverPrivateIPv4()
			if len(ips) == 0 {
				fmt.Println("No private LAN IPv4 addresses detected — this host may only have public or loopback interfaces.")
				return nil
			}
			fmt.Printf("LAN access — detected %d IPv4 address(es) on this device:\n", len(ips))
			urls := make([]string, len(ips))
			for i, ip := range ips {
				urls[i] = fmt.Sprintf("http://%s:%d", ip, port)
				fmt.Printf("    [%d] %s\n", i+1, urls[i])
			}
			fmt.Println()
			fmt.Println("  Whitelist for browser access from other devices?")
			fmt.Println("    a = all       n = none (default)       1,2,3 = pick by number")
			fmt.Print("  Choice [n]: ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			selected := pickURLs(line, urls)
			if len(selected) == 0 {
				fmt.Println("  Skipped — nothing added.")
				return nil
			}
			return withConfigsService(func(ctx context.Context, svc *configs.Service) error {
				added, skipped, err := addAllowedOrigins(ctx, svc, selected)
				if err != nil {
					return err
				}
				for _, u := range added {
					fmt.Printf("  ✓ added %s\n", u)
				}
				for _, u := range skipped {
					fmt.Printf("  · %s (already present)\n", u)
				}
				return nil
			})
		},
	}
}

// pickURLs resolves the autodetect choice string into a concrete URL
// list. Empty input or "n"/"none" returns nil. "a"/"all" returns
// every URL. A comma-separated list of 1-based indices returns the
// matching subset; out-of-range entries are silently dropped so a
// stray comma doesn't abort the whole pick.
func pickURLs(choice string, urls []string) []string {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "n", "no", "none":
		return nil
	case "a", "all", "yes", "y":
		return urls
	}
	out := make([]string, 0)
	seen := make(map[int]bool)
	for _, tok := range strings.Split(choice, ",") {
		tok = strings.TrimSpace(tok)
		n, err := strconv.Atoi(tok)
		if err != nil || n < 1 || n > len(urls) || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, urls[n-1])
	}
	return out
}

// addAllowedOrigins appends each URL in `urls` to the allowed_origins
// kvlist (deduping against the existing set) and writes the merged
// JSON value back. Returns (added, alreadyPresent, err).
func addAllowedOrigins(ctx context.Context, svc *configs.Service, urls []string) (added, skipped []string, err error) {
	current := svc.AllowedOrigins()
	present := make(map[string]bool, len(current))
	for _, u := range current {
		present[u] = true
	}
	added = make([]string, 0, len(urls))
	skipped = make([]string, 0)
	merged := append([]string{}, current...)
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if present[u] {
			skipped = append(skipped, u)
			continue
		}
		present[u] = true
		merged = append(merged, u)
		added = append(added, u)
	}
	if len(added) == 0 {
		return added, skipped, nil
	}
	value, err := encodeAllowedOrigins(merged)
	if err != nil {
		return nil, nil, err
	}
	if err := svc.Set(ctx, configs.KeyAllowedOrigins, value); err != nil {
		return nil, nil, err
	}
	return added, skipped, nil
}

// removeAllowedOrigins drops each URL in `urls` from allowed_origins.
// Returns the URLs that were actually removed (matches are exact).
func removeAllowedOrigins(ctx context.Context, svc *configs.Service, urls []string) ([]string, error) {
	current := svc.AllowedOrigins()
	drop := make(map[string]bool, len(urls))
	for _, u := range urls {
		if u = strings.TrimSpace(u); u != "" {
			drop[u] = true
		}
	}
	kept := make([]string, 0, len(current))
	removed := make([]string, 0)
	for _, u := range current {
		if drop[u] {
			removed = append(removed, u)
			continue
		}
		kept = append(kept, u)
	}
	if len(removed) == 0 {
		return removed, nil
	}
	value, err := encodeAllowedOrigins(kept)
	if err != nil {
		return nil, err
	}
	if err := svc.Set(ctx, configs.KeyAllowedOrigins, value); err != nil {
		return nil, err
	}
	return removed, nil
}

// encodeAllowedOrigins renders the list as the JSON kvlist shape the
// admin UI persists: [{"url":"..."}, ...]. Empty list serialises to
// "[]" so the row stays well-formed.
func encodeAllowedOrigins(urls []string) (string, error) {
	rows := make([]map[string]string, 0, len(urls))
	for _, u := range urls {
		rows = append(rows, map[string]string{"url": u})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ensure entity is referenced to silence import-pruning if the body
// later stops needing it directly.
var _ entity.Config
