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
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"fyne.io/systray"
	"github.com/rs/zerolog"

	"github.com/yogasw/wick/internal/autostart"
	"github.com/yogasw/wick/internal/mcpconfig"
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/worker"
	"github.com/yogasw/wick/internal/updater"
	"github.com/yogasw/wick/internal/userconfig"
)

var (
	mu sync.Mutex

	serverCancel context.CancelFunc
	serverDone   chan struct{}
	serverPort   int

	workerCancel context.CancelFunc
	workerDone   chan struct{}

	project     string
	appName     string
	appVersion  string
	wickVersion string
	buildCommit string
	buildTime   string
	logDir      string
	serverLogger zerolog.Logger
	workerLogger zerolog.Logger
	userCfg     userconfig.Config
	cfgPath     string

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
	project = projectDir
	if name == "" {
		name = filepath.Base(projectDir)
	}
	appName = name
	appVersion = appVer
	wickVersion = wickVer
	buildCommit = commit
	buildTime = builtAt
	serverPort = config.Load().App.Port

	// Log files first — windowsgui builds have no stderr, so any crash
	// before this point is invisible.
	if ls, cleanup, err := setupLogFiles(appName, 0); err == nil {
		logDir = ls.Dir
		serverLogger = ls.Server
		workerLogger = ls.Worker
		defer cleanup()
	}

	// Per-app PID-file lock under UserConfigDir. A live match for the
	// same exe means another copy is already in the tray — bail so we
	// don't leave two icons fighting over the same DB / port. Different
	// appNames have different files, so test-baruN doesn't lock out
	// test-baruM.
	if release, err := acquireSingleInstance(); err == nil {
		defer release()
	} else {
		log.Printf("single-instance: %v", err)
		return
	}

	if cfg, err := userconfig.Load(appName); err == nil {
		userCfg = cfg
	}
	if p, err := userconfig.Path(appName); err == nil {
		cfgPath = p
	}
	if err := userconfig.Save(appName, userCfg); err != nil {
		log.Printf("save config (initial): %v", err)
	}

	userconfig.ResolveDBPath(appName, userCfg.DatabasePath)
	userconfig.ResolvePort(userCfg.Port)
	updater.CleanupOldBinary()
	upd, err := updater.New(&userCfg, saveUserCfg, appName, appVersion, repo, pat)
	if err != nil {
		log.Printf("updater init: %v", err)
	} else {
		updaterInst = upd
		if upd.HasStaged() {
			log.Printf("applying staged update %s …", upd.StagedVersion())
			if err := upd.ApplyStagedAndRestart(stopServer, stopWorker); err != nil {
				log.Printf("apply staged: %v — continuing with current binary", err)
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
		log.Printf("save config: %v", err)
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

func onReady() {
	refreshIcon()
	systray.SetTitle(appName)
	systray.SetTooltip(appName + " — " + project)

	mInfo := systray.AddMenuItem(fmt.Sprintf("%s %s  (wick %s)", appName, fmtVer(appVersion), fmtVer(wickVersion)), project)
	mInfo.Disable()
	systray.AddSeparator()

	mServer := systray.AddMenuItem("Start server", "Toggle HTTP server")
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
						log.Printf("install %s: %v", ui.c.ID, err)
					} else {
						ui.refresh()
					}
				case <-uninstall.ClickedCh:
					if err := mcpconfig.Uninstall(ui.c, appName); err != nil {
						log.Printf("uninstall %s: %v", ui.c.ID, err)
					} else {
						ui.refresh()
					}
				case <-open.ClickedCh:
					if err := openInEditor(ui.c.Path); err != nil {
						log.Printf("open %s: %v", ui.c.Path, err)
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
			log.Printf("autostart enable: %v", err)
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
	mWickRepo := mAbout.AddSubMenuItem("Wick Repository", "https://github.com/yogasw/wick")
	mWickDocs := mAbout.AddSubMenuItem("Wick Documentation", "https://yogasw.github.io/wick/")
	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit "+appName)

	setServerLabel := func(running bool, errMsg string) {
		switch {
		case running:
			mServer.SetTitle(fmt.Sprintf("Stop server  (running on :%d)", serverPort))
		case errMsg != "":
			mServer.SetTitle("Start server  (failed: " + errMsg + ")")
		default:
			mServer.SetTitle("Start server")
		}
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
			log.Printf("auto-start server: %v", err)
			setServerLabel(false, err.Error())
		} else {
			setServerLabel(true, "")
		}
	} else {
		setServerLabel(false, "")
	}
	if userCfg.AutoStartWorker {
		if err := startWorker(); err != nil {
			log.Printf("auto-start worker: %v", err)
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
			ctx := context.Background()
			info, err := updaterInst.CheckLatest(ctx)
			if err != nil {
				log.Printf("check update: %v", err)
				mCheckUpdate.Enable()
				if strings.Contains(err.Error(), "auth failed") {
					mCheckUpdate.SetTitle("Update check failed — PAT expired (see logs)")
				} else {
					mCheckUpdate.SetTitle("Update check failed (see logs)")
				}
				return
			}
			if info.AlreadyLatest {
				mCheckUpdate.Enable()
				mCheckUpdate.SetTitle(fmt.Sprintf("Up to date (%s)", info.Version))
				return
			}
			if info.AlreadyStaged {
				mCheckUpdate.Enable()
				mCheckUpdate.SetTitle("Check for updates")
				mRestart.SetTitle(fmt.Sprintf("Restart to apply %s", info.Version))
				mRestart.Show()
				return
			}
			mCheckUpdate.SetTitle(fmt.Sprintf("New version %s — downloading…", info.Version))
			if err := updaterInst.Download(ctx, info); err != nil {
				log.Printf("download update: %v", err)
				mCheckUpdate.Enable()
				mCheckUpdate.SetTitle("Download failed (see logs)")
				return
			}
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

	go func() {
		for {
			select {
			case <-mServer.ClickedCh:
				if isServerRunning() {
					stopServer()
					setServerLabel(false, "")
				} else if err := startServer(); err != nil {
					log.Printf("start server: %v", err)
					setServerLabel(false, err.Error())
				} else {
					setServerLabel(true, "")
				}
				refreshIcon()
			case <-mWorker.ClickedCh:
				if isWorkerRunning() {
					stopWorker()
					setWorkerLabel(false)
				} else if err := startWorker(); err != nil {
					log.Printf("start worker: %v", err)
				} else {
					setWorkerLabel(true)
				}
				refreshIcon()
			case <-mLogs.ClickedCh:
				if logDir != "" {
					if err := openInEditor(logDir); err != nil {
						log.Printf("open logs: %v", err)
					}
				}
			case <-mCheckUpdate.ClickedCh:
				runCheck()
			case <-mRestart.ClickedCh:
				if updaterInst == nil {
					continue
				}
				if err := updaterInst.ApplyStagedAndRestart(stopServer, stopWorker); err != nil {
					log.Printf("apply update: %v", err)
				}
			case <-mInstallAll.ClickedCh:
				for _, ui := range uis {
					if err := installOne(ui.c); err != nil {
						log.Printf("install %s: %v", ui.c.ID, err)
					} else {
						ui.refresh()
					}
				}
			case <-mUninstallAll.ClickedCh:
				for _, ui := range uis {
					if err := mcpconfig.Uninstall(ui.c, appName); err != nil {
						log.Printf("uninstall %s: %v", ui.c.ID, err)
					} else {
						ui.refresh()
					}
				}
			case <-mAutoApp.ClickedCh:
				userCfg.AutoStartApp = !userCfg.AutoStartApp
				if userCfg.AutoStartApp {
					if err := autostart.Enable(appName); err != nil {
						log.Printf("autostart enable: %v", err)
						userCfg.AutoStartApp = false
					} else {
						mAutoApp.Check()
					}
				} else {
					if err := autostart.Disable(appName); err != nil {
						log.Printf("autostart disable: %v", err)
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
						log.Printf("open config: %v", err)
					}
				}
			case <-mExample.ClickedCh:
				path, err := writeExampleConfig()
				if err != nil {
					log.Printf("example config: %v", err)
					continue
				}
				if err := openInEditor(path); err != nil {
					log.Printf("open example: %v", err)
				}
			case <-mWickRepo.ClickedCh:
				if err := openInEditor("https://github.com/yogasw/wick"); err != nil {
					log.Printf("open wick repo: %v", err)
				}
			case <-mWickDocs.ClickedCh:
				if err := openInEditor("https://yogasw.github.io/wick/"); err != nil {
					log.Printf("open wick docs: %v", err)
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

func isServerRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return serverCancel != nil
}

func isWorkerRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return workerCancel != nil
}

func startServer() error {
	mu.Lock()
	if serverCancel != nil {
		mu.Unlock()
		return fmt.Errorf("already running")
	}

	// Pre-flight bind check so port collisions surface synchronously to
	// the caller (tray menu / auto-start). Tiny race window between
	// closing this listener and api.NewServer().Run binding the same
	// port, but the cost of getting it wrong is just a confusing error
	// message — acceptable for UX feedback on the common case.
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", serverPort))
	if err != nil {
		mu.Unlock()
		return fmt.Errorf("port %d in use: %w", serverPort, err)
	}
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ctx = serverLogger.WithContext(ctx)
	serverCancel = cancel
	serverDone = make(chan struct{})
	mu.Unlock()

	srv := api.NewServer()
	go func() {
		defer close(serverDone)
		if err := srv.Run(ctx, serverPort); err != nil {
			log.Printf("server: %v", err)
		}
		mu.Lock()
		serverCancel = nil
		mu.Unlock()
		refreshIcon()
	}()
	return nil
}

func stopServer() {
	mu.Lock()
	cancel := serverCancel
	done := serverDone
	mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func startWorker() error {
	mu.Lock()
	if workerCancel != nil {
		mu.Unlock()
		return fmt.Errorf("already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = workerLogger.WithContext(ctx)
	workerCancel = cancel
	workerDone = make(chan struct{})
	mu.Unlock()

	srv := worker.NewServer()
	go func() {
		defer close(workerDone)
		if err := srv.Run(ctx); err != nil {
			log.Printf("worker: %v", err)
		}
		mu.Lock()
		workerCancel = nil
		mu.Unlock()
		refreshIcon()
	}()
	return nil
}

func stopWorker() {
	mu.Lock()
	cancel := workerCancel
	done := workerDone
	mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

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
