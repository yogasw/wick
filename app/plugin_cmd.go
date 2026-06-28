package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/userconfig"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// withPluginStore opens the DB and runs fn against a plugin StateStore.
func withPluginStore(fn func(store *connplugin.StateStore) error) error {
	userconfig.ResolveDBPath(BuildAppName, "")
	db := postgres.NewGORM(config.Load().Database)
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	return fn(connplugin.NewStateStore(db))
}

func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plugin", Short: "Manage connector plugins"}
	cmd.AddCommand(pluginSearchCmd(), pluginInstallCmd(), pluginListCmd(), pluginRemoveCmd(), pluginEnableCmd(), pluginDisableCmd())
	return cmd
}

func pluginSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search [query]",
		Short: "List connector plugins available to install from the marketplace registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) == 1 {
				query = strings.ToLower(args[0])
			}
			reg := connplugin.DefaultRegistry()
			list, err := reg.List(cmd.Context())
			if err != nil {
				return err
			}
			host := runtime.GOOS + "/" + runtime.GOARCH
			shown := 0
			for _, a := range list {
				if query != "" && !strings.Contains(strings.ToLower(a.Name), query) &&
					!strings.Contains(strings.ToLower(a.Description), query) {
					continue
				}
				arch := "no"
				if a.AssetFor(host) != "" {
					arch = "yes"
				}
				fmt.Printf("%-20s %-10s arch:%s  %s\n", a.Name, a.Version, arch, a.Description)
				shown++
			}
			if shown == 0 {
				fmt.Println("no matching connectors in the registry")
			}
			return nil
		},
	}
}

func pluginInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <name|path|url>",
		Short: "Install a connector plugin by registry name, or from a local path, archive, or URL",
		Long: `Install a connector plugin.

  <name>  resolve the latest matching release from the marketplace registry
          (the arch-matching zip is downloaded, verified, and installed)
  <path>  a local directory containing {binary, plugin.json}
  <url>   a direct http(s) link to a .zip / .tar.gz
  <file>  a local .zip / .tar.gz archive

The marketplace catalog is a plugins.json fetched raw from the wick repo's
default branch; override its URL with WICK_PLUGIN_CATALOG.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			// Bare name (not a URL, not an existing path) → resolve via registry.
			if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
				if _, statErr := os.Stat(src); statErr != nil {
					reg := connplugin.DefaultRegistry()
					avail, url, err := reg.Resolve(cmd.Context(), src, "")
					if err != nil {
						return err
					}
					fmt.Printf("resolved %s v%s → %s\n", avail.Name, avail.Version, url)
					src = url
				}
			}
			dir, cleanup, err := connplugin.ResolveSource(cmd.Context(), src)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := connplugin.InstallFromDir(dir, connplugin.DefaultDir()); err != nil {
				return err
			}
			fmt.Println("plugin installed; a running wick will pick it up shortly")
			return nil
		},
	}
}

func pluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed connector plugins",
		RunE: func(_ *cobra.Command, _ []string) error {
			found, err := connplugin.Scan(connplugin.DefaultDir())
			if err != nil {
				return err
			}
			states := map[string]bool{}
			_ = withPluginStore(func(store *connplugin.StateStore) error {
				if m, lerr := store.List(); lerr == nil {
					states = m
				}
				return nil
			})
			host := runtime.GOOS + "/" + runtime.GOARCH
			for _, f := range found {
				signed := "none"
				if f.Manifest.Signature != "" {
					if wickplugin.VerifySHA256(wickplugin.TrustedKeys(), f.Manifest.SHA256, f.Manifest.Signature) {
						signed = "valid"
					} else {
						signed = "INVALID"
					}
				}
				archOK := "no"
				for _, a := range f.Manifest.OSArch {
					if a == host {
						archOK = "yes"
					}
				}
				status := "enabled"
				if v, ok := states[f.Key]; ok && !v {
					status = "disabled"
				}
				fmt.Printf("%-20s %-12s arch:%s signed:%s %s\n", f.Key, f.Manifest.Version, archOK, signed, status)
			}
			return nil
		},
	}
}

func pluginRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <key>",
		Short: "Remove an installed connector plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			found, err := connplugin.Scan(connplugin.DefaultDir())
			if err != nil {
				return err
			}
			for _, f := range found {
				if f.Key == args[0] {
					dir := filepath.Dir(f.BinaryPath)
					if err := os.RemoveAll(dir); err != nil {
						return err
					}
					fmt.Printf("removed plugin %q\n", args[0])
					return nil
				}
			}
			return fmt.Errorf("plugin %q not installed", args[0])
		},
	}
}

func pluginEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <key>",
		Short: "Enable a connector plugin (running wick picks it up shortly)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return withPluginStore(func(store *connplugin.StateStore) error {
				if err := store.SetEnabled(args[0], true); err != nil {
					return err
				}
				fmt.Printf("plugin %q enabled\n", args[0])
				return nil
			})
		},
	}
}

func pluginDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <key>",
		Short: "Disable a connector plugin without removing it",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return withPluginStore(func(store *connplugin.StateStore) error {
				if err := store.SetEnabled(args[0], false); err != nil {
					return err
				}
				fmt.Printf("plugin %q disabled\n", args[0])
				return nil
			})
		},
	}
}
