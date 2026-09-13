package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yogasw/wick/internal/initcreds"
	"github.com/yogasw/wick/internal/pkg/daemon"
	"github.com/yogasw/wick/internal/pkg/env"
	"github.com/yogasw/wick/internal/userconfig"
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
	var host string
	var localhost bool
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
			// systemd owns the lifecycle when a unit is installed+enabled.
			// Delegate rather than spawn a second PID-file daemon — two
			// instances would fight over the port. One jalur.
			if daemon.ServiceManaged(BuildAppName) {
				if daemon.ServiceActive(BuildAppName) {
					fmt.Printf("%s already running (via systemd)\n  manage with: systemctl --user [start|stop|restart|status] %s\n",
						BuildAppName, BuildAppName)
					return nil
				}
				if err := daemon.ServiceCtl(BuildAppName, "start"); err != nil {
					return fmt.Errorf("systemctl --user start: %w", err)
				}
				fmt.Printf("started %s (via systemd)\n  status: systemctl --user status %s\n", BuildAppName, BuildAppName)
				printInitCredsBanner(BuildAppName)
				return nil
			}
			mode := daemonArgs()
			// Propagate --host / --localhost to the spawned child via env so
			// the flag survives the detach across both `all` and `tray` modes
			// (tray boots the server in-process; setting WICK_HOST in the
			// parent before fork is the simplest way to thread it through).
			if err := applyHostFlags(host, localhost); err != nil {
				return err
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
	c.Flags().StringVar(&host, "host", "", "Bind interface (e.g. 127.0.0.1, 192.168.1.42) — default empty binds all (env: WICK_HOST)")
	c.Flags().BoolVar(&localhost, "localhost", false, "Shortcut for --host 127.0.0.1 — not reachable from LAN")
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
			// systemd-managed: an intentional `systemctl stop` is the only
			// way to halt without Restart=on-failure respawning behind us.
			// Sending SIGTERM by PID would just trigger a respawn.
			if daemon.ServiceManaged(BuildAppName) {
				if err := daemon.ServiceCtl(BuildAppName, "stop"); err != nil {
					return fmt.Errorf("systemctl --user stop: %w", err)
				}
				fmt.Printf("stopped %s (via systemd)\n", BuildAppName)
				return nil
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

// daemonReloadCmd performs a graceful, zero-downtime upgrade: the running
// process starts a successor, hands it the listening socket, and drains its
// own in-flight work before exiting.
//
// With --binary it also owns the step before the signal: verifying and
// installing the new binary at the path the successor will exec. Splitting
// those two across a shell script is how a swap silently ends up at the
// wrong path, with the wrong architecture, or with nobody checking whether
// the successor ever came up.
//
// Routing, in order: an installed systemd unit gets `systemctl --user reload`
// (which needs ExecReload in the unit); a unit without ExecReload falls back
// to signalling its MainPID; a PID-file daemon is signalled directly.
// refusalWindow is how far back a no-wait reload looks for a refusal. The
// daemon records one within milliseconds of being asked; a second of slack
// covers two processes reading the same clock.
const refusalWindow = 2 * time.Second

func daemonReloadCmd() *cobra.Command {
	var (
		binaryPath string
		wantSHA    string
		assumeYes  bool
		force      bool
		useSudo    bool
		wait       bool
		waitDrain  bool
		timeout    time.Duration
	)
	c := &cobra.Command{
		Use:     "reload",
		Aliases: []string{"upgrade-inplace"},
		Short:   "Hand over to the new binary without dropping in-flight work",
		// A refused binary is the whole point of --binary, so the refusal
		// must be the last thing on screen — not buried under a flag dump.
		SilenceUsage: true,
		Long: "Graceful upgrade of a running " + BuildAppName + " daemon.\n\n" +
			"The current process starts a successor, passes it the listening socket, and\n" +
			"then waits for its own in-flight work (agent turns, workflow runs, jobs) to\n" +
			"finish before exiting. No connection is refused and nothing is killed.\n\n" +
			"Pass --binary <path> to install a new build first: it is checked against the\n" +
			"running binary (wick module, app identity, architecture, version direction),\n" +
			"shown for confirmation, swapped in atomically, and verified once the\n" +
			"successor takes over. The candidate is never executed to identify it.\n\n" +
			"Requires the daemon to run with WICK_GRACEFUL_UPGRADE=1; otherwise the signal\n" +
			"is ignored and you should use `restart` instead.",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			if binaryPath != "" {
				return reloadWithBinary(p, reloadOpts{
					binaryPath: binaryPath,
					wantSHA:    wantSHA,
					assumeYes:  assumeYes,
					force:      force,
					useSudo:    useSudo,
					wait:       wait || waitDrain,
					waitDrain:  waitDrain,
					timeout:    timeout,
				})
			}
			return signalReload(p)
		},
	}
	c.Flags().StringVarP(&binaryPath, "binary", "b", "", "install this binary at the daemon's exec path, then hand over to it")
	c.Flags().StringVar(&wantSHA, "sha256", "", "expected SHA-256 of --binary; refuse to install anything else")
	c.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt (required when stdin is not a terminal)")
	c.Flags().BoolVar(&force, "force", false, "proceed despite blocking findings; never overrides OS/arch or a non-wick binary")
	c.Flags().BoolVar(&useSudo, "sudo", false, "run the file swap through sudo, for a root-owned target directory")
	c.Flags().BoolVar(&wait, "wait", false, "block until the successor is serving, and roll the binary back if it never gets there")
	c.Flags().BoolVar(&waitDrain, "wait-drain", false, "also wait for the previous process to finish its work and exit (implies --wait)")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "with --wait: how long to wait for the successor to take over")
	return c
}

// signalReload is the plain reload path: find the running daemon however it
// is supervised and ask it to hand over.
func signalReload(p daemon.Paths) error {
	if daemon.ServiceManaged(BuildAppName) {
		if err := daemon.ServiceCtl(BuildAppName, "reload"); err == nil {
			fmt.Printf("reloading %s (via systemd)\n  status: systemctl --user status %s\n", BuildAppName, BuildAppName)
			return nil
		}
		// No ExecReload in the unit — signal the process itself.
		if pid := daemon.ServiceMainPID(BuildAppName); pid > 0 {
			if err := daemon.ReloadPID(pid); err != nil {
				return fmt.Errorf("signal pid %d: %w", pid, err)
			}
			fmt.Printf("reload signalled to %s (pid %d)\n", BuildAppName, pid)
			fmt.Printf("  tip: add `ExecReload=/bin/kill -HUP $MAINPID` to the unit so `systemctl --user reload` works\n")
			return nil
		}
	}
	err := daemon.Reload(p)
	if errors.Is(err, daemon.ErrNotRunning) {
		fmt.Printf("%s is not running\n", BuildAppName)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("reload signalled to %s\n  watch: tail -f %s\n", BuildAppName, p.LogFile)
	return nil
}

type reloadOpts struct {
	binaryPath string
	wantSHA    string
	assumeYes  bool
	force      bool
	useSudo    bool
	// wait blocks until the successor is serving. Off by default: the boot
	// takes about a minute and a half, the old process serves throughout it,
	// and a blocked caller helps nobody — least of all an agent, whose open
	// turn is itself something the next swap would wait on.
	wait      bool
	waitDrain bool
	timeout   time.Duration
}

// reloadWithBinary installs a candidate binary and hands over to it.
//
// Order matters: everything that can reject the candidate runs before the
// file is touched, so a bad binary never reaches the path the daemon execs.
func reloadWithBinary(p daemon.Paths, o reloadOpts) error {
	target, oldPID, err := daemon.RunningTarget(p, BuildAppName)
	if errors.Is(err, daemon.ErrNotRunning) {
		return fmt.Errorf("%s is not running — install the binary and use `%s start`", BuildAppName, BuildAppName)
	}
	if err != nil {
		return err
	}

	// Checksum first: --sha256 exists because the file came from somewhere
	// the operator does not fully trust, so it is checked before anything
	// else reads or copies it.
	if o.wantSHA != "" {
		sum, err := daemon.FileSHA256(o.binaryPath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, strings.TrimSpace(o.wantSHA)) {
			return fmt.Errorf("sha256 mismatch\n  expected %s\n  actual   %s", strings.ToLower(o.wantSHA), sum)
		}
		fmt.Printf("sha256 ok: %s\n", sum)
	}

	candidate, err := daemon.InspectBinary(o.binaryPath)
	if err != nil {
		return fmt.Errorf("%s is not a readable Go binary: %w", o.binaryPath, err)
	}
	running, runErr := daemon.InspectBinary(target)
	if runErr != nil {
		fmt.Printf("note: cannot read build info of the running binary (%v) — identity checks are limited\n", runErr)
	}

	if daemon.SameBuild(candidate, running) && !o.force {
		fmt.Printf("%s is already running this exact build (%s, built %s) — nothing to do\n",
			BuildAppName, candidate.AppVersion, candidate.BuildTime)
		return nil
	}

	findings := daemon.CompareBinaries(candidate, running, runtime.GOOS, runtime.GOARCH)
	printReloadSummary(candidate, running, target, oldPID, findings)

	switch daemon.Worst(findings) {
	case daemon.SevFatal:
		return errors.New("refusing to install this binary (see FATAL above)")
	case daemon.SevBlock:
		if !o.force {
			return errors.New("refusing to install this binary (see BLOCK above); re-run with --force if that is intended")
		}
		fmt.Println("--force given: proceeding despite blocking findings")
	}

	if !o.useSudo && !daemon.Writable(target) {
		return fmt.Errorf("cannot replace %s as this user — re-run with --sudo", target)
	}

	if !o.assumeYes {
		if !stdinIsTerminal() {
			return errors.New("stdin is not a terminal and --yes was not given — refusing to swap a binary nobody confirmed")
		}
		if !promptYesNo(os.Stdin, fmt.Sprintf("swap %s and hand over?", filepath.Base(target))) {
			fmt.Println("aborted; nothing was changed")
			return nil
		}
	}

	backup, err := daemon.InstallBinary(o.binaryPath, target, o.useSudo)
	if err != nil {
		return fmt.Errorf("install %s: %w", target, err)
	}
	fmt.Printf("installed %s (previous kept at %s)\n", target, backup)
	// Remember WHAT we installed. A rollback must only undo our own install:
	// on a host where two deploys overlap, the loser's rollback otherwise
	// overwrites the winner's binary with a version nobody asked for — which
	// is exactly what happened here, 17 seconds after a good install.
	installed := daemon.FileFingerprint(target)

	if err := signalReload(p); err != nil {
		restoreAfterFailure(backup, target, o.useSudo, installed)
		return err
	}

	// Not waiting is the default. The boot takes about a minute and a half,
	// and during it the old process serves every request — so the wait buys
	// nothing except a blocked caller, and when the caller is an agent it
	// buys worse than nothing: its turn stays open, which is itself the
	// thing the swap would otherwise be waiting on.
	//
	// A refusal is still caught, because the daemon knows that immediately.
	if !o.wait {
		if why, refused := daemon.RefusedRecently(p.Dir, refusalWindow); refused {
			restoreAfterFailure(backup, target, o.useSudo, installed)
			return fmt.Errorf("the daemon did not start a successor: %s — %s was rolled back", why, target)
		}
		fmt.Printf("handover started: pid %d is booting the successor (%s -> %s)\n",
			oldPID, firstNonEmptyString(running.AppVersion, "unknown"), candidate.AppVersion)
		fmt.Println("not waiting for it; the old process keeps serving until the new one is ready")
		fmt.Println("pass --wait to block until the successor is serving (and to roll back if it never is)")
		return nil
	}

	fmt.Printf("waiting for the successor to take over (up to %s)...\n", o.timeout)
	newPID, err := daemon.WaitSuccessor(p, BuildAppName, oldPID, o.timeout)
	if err != nil {
		restoreAfterFailure(backup, target, o.useSudo, installed)
		return fmt.Errorf("%w — the previous process is still serving, so nothing is down; %s was rolled back", err, target)
	}
	if !daemon.ProcessImageIs(newPID, target) {
		fmt.Printf("WARNING: pid %d is not running %s — check whether the daemon was started from another path\n", newPID, target)
	}
	fmt.Printf("handover done: pid %d -> %d, %s -> %s\n", oldPID, newPID,
		firstNonEmptyString(running.AppVersion, "unknown"), candidate.AppVersion)

	if o.waitDrain {
		fmt.Printf("waiting for pid %d to finish its in-flight work...\n", oldPID)
		if err := daemon.WaitDrain(oldPID, o.timeout); err != nil {
			fmt.Printf("note: %v (it keeps draining on its own, bounded by WICK_DRAIN_TIMEOUT)\n", err)
			return nil
		}
		fmt.Printf("pid %d drained and exited; cron, channels and scheduled messages are on pid %d\n", oldPID, newPID)
	}
	return nil
}

// restoreAfterFailure puts the previous binary back so the NEXT reload or
// restart does not pick up a build that just failed to come up. Traffic is
// unaffected either way: the old process only steps aside once a successor
// reports ready.
//
// It refuses when the file is no longer the one this command installed.
// Rollback used to restore blindly, which turned a failed deploy into a
// DOWNGRADE of somebody else's successful one: two reloads overlapping on the
// same host, the slow one giving up minutes later and putting its old binary
// over the new one. Leaving a newer build in place is the safe half of that
// choice — it is the version an operator most recently asked for.
func restoreAfterFailure(backup, target string, useSudo bool, installed string) {
	if now := daemon.FileFingerprint(target); installed != "" && now != "" && now != installed {
		fmt.Printf("not rolling back %s: it has been replaced since this reload installed it — leaving the newer binary in place\n", target)
		return
	}
	if err := daemon.RestoreBinary(backup, target, useSudo); err != nil {
		fmt.Printf("WARNING: could not restore %s from %s: %v\n", target, backup, err)
		return
	}
	fmt.Printf("rolled back %s to the previous binary\n", target)
}

func printReloadSummary(candidate, running daemon.BinaryInfo, target string, pid int, findings []daemon.Finding) {
	fmt.Println()
	fmt.Printf("  candidate : %s\n", candidate.Path)
	fmt.Printf("  identity  : %s\n", candidate.Describe())
	if running.Path != "" {
		fmt.Printf("  running   : %s\n", running.Describe())
	}
	fmt.Printf("  platform  : %s/%s (host %s/%s)\n", candidate.GOOS, candidate.GOARCH, runtime.GOOS, runtime.GOARCH)
	if candidate.BuildTime != "" {
		fmt.Printf("  built     : %s\n", candidate.BuildTime)
	}
	fmt.Printf("  size      : %.1f MB\n", float64(candidate.Size)/(1024*1024))
	fmt.Printf("  target    : %s (argv[0] of pid %d)\n", target, pid)
	for _, f := range findings {
		if f.Severity == daemon.SevInfo {
			continue
		}
		fmt.Printf("  %-5s %-8s %s\n", f.Severity, f.Label, f.Detail)
	}
	fmt.Println()
}

// stdinIsTerminal reports whether a human is there to answer. A non-TTY
// stdin means a script is driving, and a script must say --yes explicitly
// rather than have silence read as consent.
//
// Note the mode bits are NOT enough on their own: /dev/null is a character
// device, so `cmd < /dev/null` — exactly how a deploy script runs — passes a
// naive ModeCharDevice test. Hence a real isatty where we have one, and
// promptYesNo treating EOF as "no" everywhere else.
func stdinIsTerminal() bool {
	return isTerminal(os.Stdin)
}

// promptYesNo asks a yes/no question. Enter means yes, but EOF does NOT:
// a closed or empty stdin is the absence of an answer, and reading it as
// consent is how an unattended script ends up swapping a binary nobody
// approved.
func promptYesNo(in io.Reader, question string) bool {
	fmt.Printf("%s [Y/n]: ", question)
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		fmt.Println()
		return false
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "" || answer == "y" || answer == "yes"
}

func firstNonEmptyString(a, fallback string) string {
	if a != "" {
		return a
	}
	return fallback
}

// daemonRestartCmd is `stop` + `start` in one command. Returns the
// new daemon's pid on success.
func daemonRestartCmd() *cobra.Command {
	var timeout time.Duration
	var host string
	var localhost bool
	c := &cobra.Command{
		Use:   "restart",
		Short: "Restart the " + BuildAppName + " daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := daemon.ResolvePaths(BuildAppName)
			if err != nil {
				return err
			}
			if daemon.ServiceManaged(BuildAppName) {
				if err := daemon.ServiceCtl(BuildAppName, "restart"); err != nil {
					return fmt.Errorf("systemctl --user restart: %w", err)
				}
				fmt.Printf("restarted %s (via systemd)\n  status: systemctl --user status %s\n", BuildAppName, BuildAppName)
				printInitCredsBanner(BuildAppName)
				return nil
			}
			mode := daemonArgs()
			if err := applyHostFlags(host, localhost); err != nil {
				return err
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
	c.Flags().StringVar(&host, "host", "", "Bind interface (e.g. 127.0.0.1, 192.168.1.42) — default empty binds all (env: WICK_HOST)")
	c.Flags().BoolVar(&localhost, "localhost", false, "Shortcut for --host 127.0.0.1 — not reachable from LAN")
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
			// systemd jalur: report from systemctl so the spawn source is
			// honest even though no `start` wrote the PID file. The actual
			// PID lives in run.pid (self-registered by `all` on boot).
			if daemon.ServiceManaged(BuildAppName) {
				active := daemon.ServiceActive(BuildAppName)
				state := "stopped"
				if active {
					state = "running"
				}
				fmt.Printf("%s: %s (via systemd)\n  unit:    %s.service\n", BuildAppName, state, BuildAppName)
				if pid, _, perr := daemon.ReadPID(p); perr == nil && pid != 0 {
					fmt.Printf("  pid:     %d\n", pid)
				}
				fmt.Printf("  http:    %s\n  manage:  systemctl --user [start|stop|restart] %s\n",
					httpStatus(BuildAppName), BuildAppName)
				if tail > 0 {
					fmt.Printf("\n--- last %d bytes of log ---\n", tail)
					_ = daemon.TailLog(p, tail, os.Stdout)
				}
				return nil
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
			fmt.Printf("%s: running (via %s)\n  pid:     %d\n  started: %s (%s ago)\n  log:     %s\n  pidfile: %s\n",
				BuildAppName, daemon.ReadSource(p), st.PID, st.Started.Format(time.RFC3339), uptime, st.LogFile, st.PIDFile)
			fmt.Printf("  http:    %s\n", httpStatus(BuildAppName))
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

// httpStatus probes the /health endpoint and returns a short status string.
func httpStatus(appName string) string {
	port := 9425
	if cfg, err := userconfig.Load(appName); err == nil && cfg.Port > 0 {
		port = cfg.Port
	}
	url := fmt.Sprintf("http://localhost:%d/health", port)
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return "unreachable"
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return fmt.Sprintf("ok (%s)", url)
	}
	return fmt.Sprintf("status %d (%s)", resp.StatusCode, url)
}
