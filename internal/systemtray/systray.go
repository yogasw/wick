//go:build !headless

// Package systemtray runs the OS system tray UI for a wick-powered app:
// start/stop the local HTTP server and the background job worker (both
// in-process via cancellable goroutines), and install or uninstall the
// app's MCP entry into detected MCP clients (Claude Desktop, Cursor,
// etc).
//
// Wired into downstream apps via the `tray` subcommand registered in
// app.Run(). Not a standalone main.
package systemtray

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/systray"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/autostart"
	"github.com/yogasw/wick/internal/initcreds"
	"github.com/yogasw/wick/internal/mcpconfig"
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/worker"
	"github.com/yogasw/wick/internal/processctl"
	"github.com/yogasw/wick/internal/updater"
	"github.com/yogasw/wick/internal/userconfig"
)

var (
	// Top-of-menu credential entry. Single clickable item that opens
	// INITIAL_CREDENTIALS.txt so the operator can copy the password —
	// printing it on a disabled menu row works too but blocks copy on
	// every platform's tray UI. Set in onReady, refreshed from anywhere
	// (server start, tray open) via refreshCredBanner so it reflects
	// the live file state without threading refs through callers.
	credOpenItem *systray.MenuItem
	credSepItem  *systray.MenuItem

	project      string
	appName      string
	appVersion   string
	wickVersion  string
	buildCommit  string
	buildTime    string
	logDir       string
	serverLogger zerolog.Logger
	workerLogger zerolog.Logger
	userCfg      userconfig.Config
	cfgPath      string

	updaterInst *updater.Updater
)

// Run starts the system tray and blocks until the user picks Quit.
// projectDir is the wick project directory (CWD where the binary lives).
// name is the MCP server name written into client configs (default: dir name).
// appVer / wickVer / commit / builtAt are the build-injected versions surfaced
// in the About submenu.
// repo ("owner/repo") and pat are injected by `wick build` for the
// self-updater; empty values disable it.
func Run(projectDir, name, appVer, wickVer, commit, builtAt, repo, pat string) {
	// Detach from the inherited console immediately so Explorer
	// double-clicks don't leave a flashing cmd window. No-op when there
	// was no console to begin with (Explorer launch on Windows, Linux/
	// macOS desktop launchers). Must run before any stdout/stderr writes
	// and before logs.go redirects them to the per-day log files.
	hideConsole()

	project = projectDir
	if name == "" {
		name = filepath.Base(projectDir)
	}
	appName = name
	// Server reads APP_NAME to resolve per-app paths (logs,
	// INITIAL_CREDENTIALS) under ~/.<appName>/ and to seed the display
	// name on first boot. Tray and CLI both go through the same env so
	// the two surfaces always agree.
	os.Setenv("APP_NAME", appName)
	// WICK_TRAY=1 signals "single-user local install" to the server —
	// loosens password-length floor on /profile/setup so admin/admin1
	// is acceptable for a laptop tray. CLI `wick server` runs (no tray)
	// keep the strict 8-char floor.
	os.Setenv("WICK_TRAY", "1")
	appVersion = appVer
	wickVersion = wickVer
	buildCommit = commit
	buildTime = builtAt
	processctl.SetManaged(true)
	processctl.SetPort(config.Load().App.Port)
	processctl.SetServerRunner(processctl.RunnerFunc(func(ctx context.Context) error {
		return api.NewServer().Run(ctx, processctl.ServerPort())
	}))
	processctl.SetWorkerRunner(processctl.RunnerFunc(func(ctx context.Context) error {
		return worker.NewServer().Run(ctx)
	}))

	// Log files first — hideConsole has detached the console for tray
	// launches, so any crash before this point is invisible without a
	// file sink.
	if ls, cleanup, err := setupLogFiles(appName, 0); err == nil {
		logDir = ls.Dir
		serverLogger = ls.Server
		workerLogger = ls.Worker
		processctl.SetServerLogger(ls.Server)
		processctl.SetWorkerLogger(ls.Worker)
		processctl.SetMCPLogger(ls.MCP)
		defer cleanup()
		log.Info().Str("app", appName).Str("version", appVer).Str("wick", wickVer).Msg("tray starting")
	}

	// Per-app PID-file lock under ~/.<appName>. A live match for the
	// same exe means another copy is already in the tray — bail so we
	// don't leave two icons fighting over the same DB / port. Different
	// appNames have different files, so test-baruN doesn't lock out
	// test-baruM.
	if release, err := acquireSingleInstance(); err == nil {
		defer release()
	} else {
		log.Error().Err(err).Msg("single-instance")
		return
	}

	if cfg, err := userconfig.Load(appName); err == nil {
		userCfg = cfg
	}
	if p, err := userconfig.Path(appName); err == nil {
		cfgPath = p
	}
	if err := userconfig.Save(appName, userCfg); err != nil {
		log.Error().Err(err).Msg("save config (initial)")
	}

	userconfig.ResolveDBPath(appName, userCfg.DatabasePath)
	userconfig.ResolvePort(userCfg.Port)
	updater.CleanupOldBinary()
	upd, err := updater.New(&userCfg, saveUserCfg, appName, appVersion, repo, pat)
	if err != nil {
		log.Error().Err(err).Msg("updater init")
	} else {
		updaterInst = upd
		if upd.HasStaged() {
			log.Info().Str("version", upd.StagedVersion()).Msg("applying staged update")
			if err := upd.ApplyStagedAndRestart(stopServer, stopWorker); err != nil {
				log.Error().Err(err).Msg("apply staged — continuing with current binary")
			}
		}
	}

	systray.Run(onReady, onExit)
}

func fmtVer(v string) string {
	if v == "" {
		return "?"
	}
	if v == "dev" || v == "unknown" {
		return v
	}
	return "v" + strings.TrimPrefix(v, "v")
}

func saveUserCfg() error {
	if err := userconfig.Save(appName, userCfg); err != nil {
		log.Error().Err(err).Msg("save config")
		return err
	}
	return nil
}

type clientUI struct {
	c   mcpconfig.Client
	sub *systray.MenuItem
}

func (u *clientUI) refresh() {
	present, installed := mcpconfig.IsInstalled(u.c, appName)
	switch {
	case !present:
		u.sub.SetTitle(u.c.Label + " — not configured yet")
	case installed:
		u.sub.SetTitle(u.c.Label + "  ✓ installed")
	default:
		u.sub.SetTitle(u.c.Label + " — not installed")
	}
}

func refreshIcon() {
	systray.SetIcon(WickIcon(isServerRunning(), isWorkerRunning(), runtime.GOOS == "windows"))
}

// refreshCredBanner syncs the three top-of-menu items (email, password,
// trailing separator) with the on-disk INITIAL_CREDENTIALS.txt. File
// missing or unreadable → hide the whole banner. Called on first
// render, on every TrayOpenedCh tick, and after server start (server
// is what writes the file on first boot).
//
// Server start is async: api.Server.Run boots in a goroutine and the
// file lands a tick later when configs.Bootstrap completes. Calling
// this synchronously right after startServer() would race that write,
// so post-start callers spawn the polled variant instead.
// refreshCredBanner toggles the "Open default password" entry based on
// the credentials file. Visibility also requires the server to be up —
// the entry lives inside the server-controls group, so hiding it when
// the server is down keeps the menu compact. The trailing separator is
// driven by setServerLabel, not here.
func refreshCredBanner() {
	if credOpenItem == nil {
		return
	}
	_, ok := initcreds.Read(appName)
	if !ok || !isServerRunning() {
		credOpenItem.Hide()
		return
	}
	credOpenItem.Show()
}

// credAutoOpened guards the once-per-session auto-open. Without it
// every server start would re-launch Notepad, which is annoying for
// the operator who restarts the server while debugging.
var credAutoOpened bool

// refreshCredBannerSoon polls the credentials file for ~5s after server
// boot, applying the banner the moment the file appears. Stops early
// when the banner is already up. The first time the file shows up in a
// session it also opens the file in the default editor so a brand-new
// install leads the operator straight to the password to copy.
// Single-shot — caller fires one and forgets, no overlapping polls.
func refreshCredBannerSoon() {
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, ok := initcreds.Read(appName); ok {
				refreshCredBanner()
				autoOpenCredsOnce()
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		refreshCredBanner()
	}()
}

func autoOpenCredsOnce() {
	if credAutoOpened {
		return
	}
	credAutoOpened = true
	path, err := initcreds.Path(appName)
	if err != nil {
		return
	}
	if err := openInEditor(path); err != nil {
		log.Warn().Err(err).Msg("auto-open initial credentials")
	}
}

// refreshInitCredsItem hides the About → "Open initial credentials"
// entry when the file no longer exists. Mirrors applyCredItems for the
// secondary entry point so both surfaces stay in sync.
func refreshInitCredsItem(item *systray.MenuItem) {
	path, err := initcreds.Path(appName)
	if err != nil || path == "" {
		item.Hide()
		return
	}
	if _, err := os.Stat(path); err != nil {
		item.Hide()
		return
	}
	item.Show()
}

func onReady() {
	refreshIcon()
	systray.SetTitle(appName)
	systray.SetTooltip(appName + " — " + project)

	// Refresh tray icon every time processctl flips state. Covers
	// MCP-driven Start/Stop (wick_manager_system_*) so the icon
	// matches reality without forcing the user to re-open the menu.
	processctl.Subscribe(func(_ processctl.StateChange) { refreshIcon() })

	// One-shot launch toast so user sees the app actually started in the
	// tray (icon alone is easy to miss). Fires per process start, not on
	// menu clicks — re-open after Quit shows it again.
	go func() {
		if err := notify(appName+" running", "Running in the system tray."); err != nil {
			log.Warn().Err(err).Msg("launch notify")
		}
	}()

	mInfo := systray.AddMenuItem(fmt.Sprintf("%s %s  (wick %s)", appName, fmtVer(appVersion), fmtVer(wickVersion)), project)
	mInfo.Disable()
	systray.AddSeparator()

	// Server controls. When the server is up the related actions
	// (Open URL, Open default password) cluster underneath, separated
	// from the worker row below — keeps the "doing things with the
	// server" group visually distinct once the menu grows.
	mServer := systray.AddMenuItem("Start server", "Toggle HTTP server")
	mOpenURL := systray.AddMenuItem("Open server URL", "Open the server in your browser")
	mOpenURL.Hide()
	credOpenItem = systray.AddMenuItem("Open default password", "Open INITIAL_CREDENTIALS.txt to copy the auto-generated admin password")
	credSepItem = systray.AddMenuItem("─────────────", "")
	credSepItem.Disable()
	credSepItem.Hide()
	mWorker := systray.AddMenuItem("Start worker", "Toggle background job worker")

	// Update controls
	mCheckUpdate := systray.AddMenuItem("Check for updates", "Fetch latest release from GitHub")
	mRestart := systray.AddMenuItem("", "Apply downloaded update and restart")
	if updaterInst != nil && updaterInst.HasStaged() {
		mRestart.SetTitle(fmt.Sprintf("Restart to apply %s", updaterInst.StagedVersion()))
	} else {
		mRestart.Hide()
	}
	updaterReady := updaterInst != nil && updaterInst.Configured()
	if !updaterReady {
		mCheckUpdate.Hide()
	}
	systray.AddSeparator()

	mMCP := systray.AddMenuItem("MCP", "Install MCP entry into client config")
	mInstallAll := mMCP.AddSubMenuItem("Install all detected", "Install into every detected client")
	mUninstallAll := mMCP.AddSubMenuItem("Uninstall all", "Remove from every detected client")
	mExample := mMCP.AddSubMenuItem("Show example config", "Open generated MCP config snippet in editor")
	mMCP.AddSubMenuItem("─────────────", "").Disable()

	clients := mcpconfig.Detected(project)
	uis := make([]*clientUI, 0, len(clients))
	for i := range clients {
		c := clients[i]
		ui := &clientUI{c: c, sub: mMCP.AddSubMenuItem(c.Label, c.Path)}
		ui.refresh()
		uis = append(uis, ui)
		install := ui.sub.AddSubMenuItem("Install / update", "Write entry into "+c.Path)
		uninstall := ui.sub.AddSubMenuItem("Uninstall", "Remove entry from "+c.Path)
		open := ui.sub.AddSubMenuItem("Open config", "Open "+c.Path)
		go func(ui *clientUI, install, uninstall, open *systray.MenuItem) {
			for {
				select {
				case <-install.ClickedCh:
					if err := installOne(ui.c); err != nil {
						log.Error().Str("client", ui.c.ID).Err(err).Msg("mcp install")
					} else {
						log.Info().Str("client", ui.c.ID).Str("path", ui.c.Path).Msg("mcp installed")
						ui.refresh()
					}
				case <-uninstall.ClickedCh:
					if err := mcpconfig.Uninstall(ui.c, appName); err != nil {
						log.Error().Str("client", ui.c.ID).Err(err).Msg("mcp uninstall")
					} else {
						log.Info().Str("client", ui.c.ID).Str("path", ui.c.Path).Msg("mcp uninstalled")
						ui.refresh()
					}
				case <-open.ClickedCh:
					if err := openInEditor(ui.c.Path); err != nil {
						log.Error().Str("path", ui.c.Path).Err(err).Msg("open config")
					}
				}
			}
		}(ui, install, uninstall, open)
	}
	systray.AddSeparator()

	mPrefs := systray.AddMenuItem("Preferences", "Per-machine settings (saved to "+cfgPath+")")
	// Sync OS-level autostart with config — handles binary moved/renamed
	// (re-Enable refreshes the path; user toggling won't notice the diff).
	if userCfg.AutoStartApp {
		if err := autostart.Enable(appName); err != nil {
			log.Error().Err(err).Msg("autostart enable")
		}
	}
	mPrefs.AddSubMenuItem("── Launch ──", "").Disable()
	mAutoApp := mPrefs.AddSubMenuItemCheckbox("Auto-start app at login", "Launch this binary at OS user login", userCfg.AutoStartApp)
	mAutoSrv := mPrefs.AddSubMenuItemCheckbox("Auto-start server on launch", "Start HTTP server immediately when tray opens", userCfg.AutoStartServer)
	mAutoWrk := mPrefs.AddSubMenuItemCheckbox("Auto-start worker on launch", "Start background worker immediately when tray opens", userCfg.AutoStartWorker)
	mPrefs.AddSubMenuItem("── Updates ──", "").Disable()
	mAutoUpd := mPrefs.AddSubMenuItemCheckbox("Auto-update", "Check + download new releases in background", userCfg.AutoUpdate)
	mPrefs.AddSubMenuItem("── Config ──", "").Disable()
	mOpenCfg := mPrefs.AddSubMenuItem("Open config file", "Open "+cfgPath)
	if cfgPath == "" {
		mOpenCfg.Disable()
	}
	systray.AddSeparator()

	// About submenu
	mAbout := systray.AddMenuItem("About", "")
	mAbout.AddSubMenuItem(fmt.Sprintf("App:    %s %s", appName, fmtVer(appVersion)), "").Disable()
	mAbout.AddSubMenuItem(fmt.Sprintf("Wick:   %s", fmtVer(wickVersion)), "").Disable()
	mAbout.AddSubMenuItem(fmt.Sprintf("Commit: %s", fmtBuildField(buildCommit)), "").Disable()
	mAbout.AddSubMenuItem(fmt.Sprintf("Built:  %s", fmtBuildField(buildTime)), "").Disable()
	if !updaterReady {
		mAbout.AddSubMenuItem("Updates: not configured", "Build with --release-github-repo <owner>/<repo> or use a github.com/owner/repo module path to enable self-update").Disable()
	}
	mAbout.AddSubMenuItem("─────────────", "").Disable()
	mLogs := mAbout.AddSubMenuItem("Open logs", "Open "+logDir)
	if logDir == "" {
		mLogs.Disable()
	}
	// Visible only while INITIAL_CREDENTIALS.txt exists — first-login
	// setup deletes the file, which hides the menu entry on next refresh.
	credPath, _ := initcreds.Path(appName)
	mInitCreds := mAbout.AddSubMenuItem("Open initial credentials", "Show the auto-generated admin password")
	if credPath == "" {
		mInitCreds.Hide()
	} else if _, err := os.Stat(credPath); err != nil {
		mInitCreds.Hide()
	}
	mWickRepo := mAbout.AddSubMenuItem("Wick Repository", "https://github.com/yogasw/wick")
	mWickDocs := mAbout.AddSubMenuItem("Wick Documentation", "https://yogasw.github.io/wick/")
	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit "+appName)

	setServerLabel := func(running bool, errMsg string) {
		switch {
		case running:
			mServer.SetTitle(fmt.Sprintf("Stop server  (running on :%d)", processctl.ServerPort()))
			mOpenURL.Show()
			credSepItem.Show()
		case errMsg != "":
			mServer.SetTitle("Start server  (failed: " + errMsg + ")")
			mOpenURL.Hide()
			credSepItem.Hide()
		default:
			mServer.SetTitle("Start server")
			mOpenURL.Hide()
			credSepItem.Hide()
		}
		refreshCredBanner()
	}
	setWorkerLabel := func(running bool) {
		if running {
			mWorker.SetTitle("Stop worker  (running)")
		} else {
			mWorker.SetTitle("Start worker")
		}
	}

	if userCfg.AutoStartServer {
		if err := startServer(); err != nil {
			log.Error().Err(err).Msg("auto-start server")
			setServerLabel(false, err.Error())
		} else {
			setServerLabel(true, "")
			refreshCredBannerSoon()
		}
	} else {
		setServerLabel(false, "")
	}
	if userCfg.AutoStartWorker {
		if err := startWorker(); err != nil {
			log.Error().Err(err).Msg("auto-start worker")
			setWorkerLabel(false)
		} else {
			setWorkerLabel(true)
		}
	} else {
		setWorkerLabel(false)
	}
	refreshIcon()

	// runCheck performs a manual update check with stepwise UI feedback:
	// Checking… → New version vX — downloading… → Restart to apply vX (or error).
	runCheck := func() {
		if updaterInst == nil {
			return
		}
		mCheckUpdate.SetTitle("Checking for updates…")
		mCheckUpdate.Disable()
		go func() {
			log.Info().Msg("update: checking latest")
			ctx := context.Background()
			info, err := updaterInst.CheckLatest(ctx)
			if err != nil {
				log.Error().Err(err).Msg("update check")
				mCheckUpdate.Enable()
				if strings.Contains(err.Error(), "auth failed") {
					mCheckUpdate.SetTitle("Update check failed — PAT expired (see logs)")
				} else {
					mCheckUpdate.SetTitle("Update check failed (see logs)")
				}
				return
			}
			if info.AlreadyLatest {
				log.Info().Str("version", info.Version).Msg("update: already latest")
				mCheckUpdate.Enable()
				mCheckUpdate.SetTitle(fmt.Sprintf("Up to date (%s)", info.Version))
				return
			}
			if info.AlreadyStaged {
				log.Info().Str("version", info.Version).Msg("update: already staged")
				mCheckUpdate.Enable()
				mCheckUpdate.SetTitle("Check for updates")
				mRestart.SetTitle(fmt.Sprintf("Restart to apply %s", info.Version))
				mRestart.Show()
				return
			}
			log.Info().Str("version", info.Version).Msg("update: downloading")
			mCheckUpdate.SetTitle(fmt.Sprintf("New version %s — downloading…", info.Version))
			if err := updaterInst.Download(ctx, info); err != nil {
				log.Error().Str("version", info.Version).Err(err).Msg("update download")
				mCheckUpdate.Enable()
				mCheckUpdate.SetTitle("Download failed (see logs)")
				return
			}
			log.Info().Str("version", info.Version).Msg("update: downloaded, restart to apply")
			mCheckUpdate.Enable()
			mCheckUpdate.SetTitle("Check for updates")
			mRestart.SetTitle(fmt.Sprintf("Restart to apply %s", info.Version))
			mRestart.Show()
		}()
	}

	// Background auto-update reuses runCheck so the menu shows the
	// same state machine (Checking… → Up to date / Restart now / error).
	if updaterInst != nil && updaterInst.Configured() && !updaterInst.HasStaged() && userCfg.AutoUpdate {
		runCheck()
	}

	// Refresh state every time the user opens the tray. Catches the
	// case where /profile/setup deleted INITIAL_CREDENTIALS.txt while
	// the tray was already running — without this, the email/password
	// banner would linger until the next process restart.
	go func() {
		for range systray.TrayOpenedCh {
			refreshCredBanner()
			refreshInitCredsItem(mInitCreds)
		}
	}()

	go func() {
		for {
			select {
			case <-mServer.ClickedCh:
				log.Info().Bool("running", isServerRunning()).Msg("menu: server toggle")
				if isServerRunning() {
					stopServer()
					setServerLabel(false, "")
				} else if err := startServer(); err != nil {
					log.Error().Err(err).Msg("start server")
					setServerLabel(false, err.Error())
				} else {
					setServerLabel(true, "")
					// Server writes INITIAL_CREDENTIALS on first boot
					// — poll for the file so the email + password rows
					// appear without waiting for the user to re-open
					// the tray.
					refreshCredBannerSoon()
				}
				refreshIcon()
			case <-mOpenURL.ClickedCh:
				log.Info().Bool("running", isServerRunning()).Int("port", processctl.ServerPort()).Msg("menu: open server url")
				if isServerRunning() {
					url := fmt.Sprintf("http://localhost:%d", processctl.ServerPort())
					if err := openInEditor(url); err != nil {
						log.Error().Str("url", url).Err(err).Msg("open server url")
					} else {
						log.Info().Str("url", url).Msg("open server url: launched")
					}
				}
			case <-credOpenItem.ClickedCh:
				path, err := initcreds.Path(appName)
				log.Info().Str("path", path).Err(err).Msg("menu: open default password")
				if err == nil {
					if err := openInEditor(path); err != nil {
						log.Error().Str("path", path).Err(err).Msg("open initial credentials")
					} else {
						log.Info().Str("path", path).Msg("open initial credentials: launched")
					}
				}
			case <-mWorker.ClickedCh:
				log.Info().Bool("running", isWorkerRunning()).Msg("menu: worker toggle")
				if isWorkerRunning() {
					stopWorker()
					setWorkerLabel(false)
				} else if err := startWorker(); err != nil {
					log.Error().Err(err).Msg("start worker")
				} else {
					setWorkerLabel(true)
				}
				refreshIcon()
			case <-mLogs.ClickedCh:
				log.Info().Str("logDir", logDir).Msg("menu: open logs")
				if logDir != "" {
					if err := openInEditor(logDir); err != nil {
						log.Error().Str("logDir", logDir).Err(err).Msg("open logs")
					} else {
						log.Info().Str("logDir", logDir).Msg("open logs: launched")
					}
				}
			case <-mInitCreds.ClickedCh:
				log.Info().Str("path", credPath).Msg("menu: open initial credentials (about)")
				if credPath != "" {
					if err := openInEditor(credPath); err != nil {
						log.Error().Str("path", credPath).Err(err).Msg("open initial credentials")
					} else {
						log.Info().Str("path", credPath).Msg("open initial credentials: launched")
					}
				}
			case <-mCheckUpdate.ClickedCh:
				runCheck()
			case <-mRestart.ClickedCh:
				if updaterInst == nil {
					continue
				}
				if err := updaterInst.ApplyStagedAndRestart(stopServer, stopWorker); err != nil {
					log.Error().Err(err).Msg("apply update")
				}
			case <-mInstallAll.ClickedCh:
				for _, ui := range uis {
					if err := installOne(ui.c); err != nil {
						log.Error().Str("client", ui.c.ID).Err(err).Msg("mcp install")
					} else {
						log.Info().Str("client", ui.c.ID).Str("path", ui.c.Path).Msg("mcp installed")
						ui.refresh()
					}
				}
			case <-mUninstallAll.ClickedCh:
				for _, ui := range uis {
					if err := mcpconfig.Uninstall(ui.c, appName); err != nil {
						log.Error().Str("client", ui.c.ID).Err(err).Msg("mcp uninstall")
					} else {
						log.Info().Str("client", ui.c.ID).Str("path", ui.c.Path).Msg("mcp uninstalled")
						ui.refresh()
					}
				}
			case <-mAutoApp.ClickedCh:
				userCfg.AutoStartApp = !userCfg.AutoStartApp
				if userCfg.AutoStartApp {
					if err := autostart.Enable(appName); err != nil {
						log.Error().Err(err).Msg("autostart enable")
						userCfg.AutoStartApp = false
					} else {
						mAutoApp.Check()
					}
				} else {
					if err := autostart.Disable(appName); err != nil {
						log.Error().Err(err).Msg("autostart disable")
					}
					mAutoApp.Uncheck()
				}
				_ = saveUserCfg()
			case <-mAutoSrv.ClickedCh:
				userCfg.AutoStartServer = !userCfg.AutoStartServer
				if userCfg.AutoStartServer {
					mAutoSrv.Check()
				} else {
					mAutoSrv.Uncheck()
				}
				_ = saveUserCfg()
			case <-mAutoWrk.ClickedCh:
				userCfg.AutoStartWorker = !userCfg.AutoStartWorker
				if userCfg.AutoStartWorker {
					mAutoWrk.Check()
				} else {
					mAutoWrk.Uncheck()
				}
				_ = saveUserCfg()
			case <-mAutoUpd.ClickedCh:
				userCfg.AutoUpdate = !userCfg.AutoUpdate
				if userCfg.AutoUpdate {
					mAutoUpd.Check()
				} else {
					mAutoUpd.Uncheck()
				}
				_ = saveUserCfg()
			case <-mOpenCfg.ClickedCh:
				if cfgPath != "" {
					if err := openInEditor(cfgPath); err != nil {
						log.Error().Err(err).Msg("open config")
					}
				}
			case <-mExample.ClickedCh:
				path, err := writeExampleConfig()
				if err != nil {
					log.Error().Err(err).Msg("example config")
					continue
				}
				if err := openInEditor(path); err != nil {
					log.Error().Err(err).Msg("open example config")
				}
			case <-mWickRepo.ClickedCh:
				if err := openInEditor("https://github.com/yogasw/wick"); err != nil {
					log.Error().Err(err).Msg("open wick repo")
				}
			case <-mWickDocs.ClickedCh:
				if err := openInEditor("https://yogasw.github.io/wick/"); err != nil {
					log.Error().Err(err).Msg("open wick docs")
				}
			case <-mQuit.ClickedCh:
				stopServer()
				stopWorker()
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {
	stopServer()
	stopWorker()
}

func fmtBuildField(v string) string {
	if v == "" || v == "unknown" {
		return "unknown"
	}
	return v
}

func isServerRunning() bool { return processctl.IsServerRunning() }
func isWorkerRunning() bool { return processctl.IsWorkerRunning() }

func startServer() error { return processctl.StartServer() }
func stopServer()        { _ = processctl.StopServer() }
func startWorker() error { return processctl.StartWorker() }
func stopWorker()        { _ = processctl.StopWorker() }

func installOne(c mcpconfig.Client) error {
	entry, err := mcpconfig.SelfEntry()
	if err != nil {
		return err
	}
	return mcpconfig.Install(c, appName, entry)
}

func writeExampleConfig() (string, error) {
	entry, err := mcpconfig.SelfEntry()
	if err != nil {
		return "", err
	}
	snippet := map[string]any{
		"mcpServers": map[string]any{appName: entry},
	}
	out, err := json.MarshalIndent(snippet, "", "  ")
	if err != nil {
		return "", err
	}
	dst := filepath.Join(os.TempDir(), appName+"-mcp-config.json")
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		return "", err
	}
	return dst, nil
}
