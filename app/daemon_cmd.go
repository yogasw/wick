package app

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/initcreds"
	"github.com/yogasw/wick/internal/pkg/daemon"
	"github.com/yogasw/wick/internal/pkg/env"
)

// printInitCredsBanner prints the same App URL / email / default-password
// block the foreground `server` / `all` commands emit on startup, so the
// operator who ran `<app> start` sees the credentials without having to
// tail daemon.log. Silent no-op when the credentials file is missing —
// either the admin password has been changed (file cleared by the
// server) or the daemon never reached the seed step.
//
// Wait window: the file is written inside the spawned daemon AFTER the
// server boots, so on first start we poll for up to ~3s. On re-runs
// the file already exists and the first iteration returns immediately.
func printInitCredsBanner(appName string) {
	credsPath, _ := initcreds.Path(appName)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if info, ok := initcreds.Read(appName); ok {
			fmt.Println()
			fmt.Printf("  → App URL:          %s\n", info.URL)
			fmt.Printf("  → Email:            %s\n", info.Email)
			fmt.Printf("  → Default password: %s\n", info.Password)
			if credsPath != "" {
				fmt.Printf("  → Saved to:         %s (auto-deleted after password change)\n", credsPath)
			}
			fmt.Printf("\n  ⚠ WARNING: Change the default password at %s/profile/setup\n", info.URL)
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// daemonArgs picks the subcommand to detach into:
//
//	GUI host  → "tray" (interactive icon, autostart toggle inside)
//	Headless  → "all"  (server + worker, no UI)
//
// Centralised so start / restart agree on the mode.
func daemonArgs() []string {
	if env.HasGUI() {
		return []string{"tray"}
	}
	return []string{"all"}
}

// daemonStartCmd spawns the binary detached from the caller's
// shell. Mode is chosen at runtime — tray on GUI hosts, `all`
// (server + worker, headless) elsewhere — so the same `start`
// command is the canonical "run in the background" entry point
// regardless of platform.
func daemonStartCmd() *cobra.Command {
	var localhostOnly bool
	c := &cobra.Command{
		Use:   "start",
		Short: "Start " + BuildAppName + " in the background (tray on GUI, daemon on headless)",
		Long: "Spawn " + BuildAppName + " detached from this shell. " +
			"Writes a PID file under the per-app dir; use `stop` / `status` / " +
			"`restart` to manage the running instance.\n\n" +
			"GUI hosts (Windows / macOS / desktop Linux) get the interactive " +
			"tray icon. Headless hosts (Termux / SSH server / no DISPLAY) get " +
			"the server + worker `all` mode with no UI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			mode := daemonArgs()
			// Propagate --localhost to the spawned child via env so the
			// flag survives the detach across both `all` and `tray` modes
			// (tray boots the server in-process; setting WICK_HOST in the
			// parent before fork is the simplest way to thread it through).
			if localhostOnly {
				_ = os.Setenv("WICK_HOST", "127.0.0.1")
			}
			pid, err := daemon.Start(p, mode)
			if errors.Is(err, daemon.ErrAlreadyRunning) {
				fmt.Printf("%s already running (pid %d). Tail log: %s\n", BuildAppName, pid, p.LogFile)
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Printf("started %s as `%s` (pid %d)\n  log: %s\n  pid: %s\n",
				BuildAppName, mode[0], pid, p.LogFile, p.PIDFile)
			fmt.Printf("  view logs: tail -f %s   (or `%s status --log 4000`)\n",
				p.LogFile, BuildAppName)
			printInitCredsBanner(BuildAppName)
			return nil
		},
	}
	c.Flags().BoolVar(&localhostOnly, "localhost", false, "Bind 127.0.0.1 only — not reachable from LAN (env: WICK_HOST=127.0.0.1)")
	return c
}

// daemonStopCmd sends SIGTERM to the daemon, waits up to 5s for
// graceful exit, then force-kills if needed.
func daemonStopCmd() *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "stop",
		Short: "Stop the running " + BuildAppName + " daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			err = daemon.Stop(p, timeout)
			if errors.Is(err, daemon.ErrNotRunning) {
				fmt.Printf("%s is not running\n", BuildAppName)
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Printf("stopped %s\n", BuildAppName)
			return nil
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "grace period before SIGKILL")
	return c
}

// daemonRestartCmd is `stop` + `start` in one command. Returns the
// new daemon's pid on success.
func daemonRestartCmd() *cobra.Command {
	var timeout time.Duration
	var localhostOnly bool
	c := &cobra.Command{
		Use:   "restart",
		Short: "Restart the " + BuildAppName + " daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			mode := daemonArgs()
			if localhostOnly {
				_ = os.Setenv("WICK_HOST", "127.0.0.1")
			}
			pid, err := daemon.Restart(p, timeout, mode)
			if err != nil {
				return err
			}
			fmt.Printf("restarted %s as `%s` (pid %d)\n  log: %s\n",
				BuildAppName, mode[0], pid, p.LogFile)
			fmt.Printf("  view logs: tail -f %s   (or `%s status --log 4000`)\n",
				p.LogFile, BuildAppName)
			printInitCredsBanner(BuildAppName)
			return nil
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "grace period before SIGKILL during stop")
	c.Flags().BoolVar(&localhostOnly, "localhost", false, "Bind 127.0.0.1 only — not reachable from LAN (env: WICK_HOST=127.0.0.1)")
	return c
}

// serviceCmd groups install / uninstall / status for OS-level
// auto-start integration (systemd-user on Linux, Termux:Boot on
// Termux, schtasks on Windows, LaunchAgent on macOS). All backends
// install into per-user scope so no sudo / admin is required.
func serviceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "service",
		Short: "Manage OS auto-start (login items on GUI, systemd-user / Termux:Boot on headless)",
	}
	c.AddCommand(serviceInstallCmd(), serviceUninstallCmd(), serviceStatusCmd())
	return c
}

func serviceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register " + BuildAppName + " to start automatically at login / boot (Linux / Termux)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			if err := daemon.InstallService(p, BuildAppName); err != nil {
				return err
			}
			st, _ := daemon.ServiceStatus(p, BuildAppName)
			fmt.Printf("installed %s service\n  backend: %s\n  path:    %s\n", BuildAppName, st.Backend, st.Path)
			if st.Note != "" {
				fmt.Printf("  note:    %s\n", st.Note)
			}
			return nil
		},
	}
}

func serviceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove " + BuildAppName + " from auto-start (Linux / Termux)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			err = daemon.UninstallService(p, BuildAppName)
			if errors.Is(err, daemon.ErrNotInstalled) {
				fmt.Printf("%s service not installed\n", BuildAppName)
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Printf("uninstalled %s service\n", BuildAppName)
			return nil
		},
	}
}

func serviceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show " + BuildAppName + " auto-start status",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			st, err := daemon.ServiceStatus(p, BuildAppName)
			if err != nil {
				return err
			}
			if !st.Installed {
				fmt.Printf("%s service: not installed\n  backend (would use): %s\n", BuildAppName, st.Backend)
				if st.Note != "" {
					fmt.Printf("  note: %s\n", st.Note)
				}
				return nil
			}
			fmt.Printf("%s service: installed\n  backend: %s\n  path:    %s\n  active:  %v\n", BuildAppName, st.Backend, st.Path, st.Active)
			if st.Note != "" {
				fmt.Printf("  note:    %s\n", st.Note)
			}
			return nil
		},
	}
}

// daemonStatusCmd prints whether the daemon is running, its PID,
// approximate uptime (from the PID file mtime), and the log file
// path. Use `--log <n>` to tail the last N bytes of the log.
func daemonStatusCmd() *cobra.Command {
	var tail int64
	c := &cobra.Command{
		Use:   "status",
		Short: "Show " + BuildAppName + " daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			st, err := daemon.Check(p)
			if err != nil {
				return err
			}
			if !st.Running {
				if st.PID != 0 {
					fmt.Printf("%s: stale PID file (last pid %d, no longer alive)\n", BuildAppName, st.PID)
				} else {
					fmt.Printf("%s: not running\n", BuildAppName)
				}
				return nil
			}
			uptime := time.Since(st.Started).Truncate(time.Second)
			fmt.Printf("%s: running\n  pid:     %d\n  started: %s (%s ago)\n  log:     %s\n  pidfile: %s\n",
				BuildAppName, st.PID, st.Started.Format(time.RFC3339), uptime, st.LogFile, st.PIDFile)
			if tail > 0 {
				fmt.Printf("\n--- last %d bytes of log ---\n", tail)
				_ = daemon.TailLog(p, tail, os.Stdout)
			}
			return nil
		},
	}
	c.Flags().Int64Var(&tail, "log", 0, "tail last N bytes of the daemon log")
	return c
}
