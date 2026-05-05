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
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"fyne.io/systray"
	"github.com/gen2brain/beeep"

	"github.com/yogasw/wick/internal/mcpconfig"
	"github.com/yogasw/wick/internal/pkg/api"
	"github.com/yogasw/wick/internal/pkg/config"
	"github.com/yogasw/wick/internal/pkg/worker"
	"github.com/yogasw/wick/internal/userconfig"
)

var (
	mu sync.Mutex

	serverCancel context.CancelFunc
	serverDone   chan struct{}
	serverPort   int

	workerCancel context.CancelFunc
	workerDone   chan struct{}

	project  string
	appName  string
	logPath  string
	userCfg  userconfig.Config
	cfgPath  string
)

// Run starts the system tray and blocks until the user picks Quit.
// projectDir is the wick project directory (CWD where the binary lives).
// name is the MCP server name written into client configs (default: dir name).
func Run(projectDir, name string) {
	project = projectDir
	if name == "" {
		name = filepath.Base(projectDir)
	}
	appName = name
	serverPort = config.Load().App.Port

	lock, err := acquireSingleInstance()
	if err != nil {
		notify(appName+" already running", "A tray instance is already active — open it from the system tray.")
		log.Printf("single-instance: %v", err)
		return
	}
	defer lock.Close()

	if cfg, err := userconfig.Load(appName); err == nil {
		userCfg = cfg
	}
	if p, err := userconfig.Path(appName); err == nil {
		cfgPath = p
	}
	// Persist defaults on first launch so "Open config file" always
	// has a real file to open (instead of Windows complaining about
	// a missing path).
	if err := userconfig.Save(appName, userCfg); err != nil {
		log.Printf("save config (initial): %v", err)
	}

	if p, cleanup, err := setupLogFile(appName); err == nil {
		logPath = p
		defer cleanup()
	}

	systray.Run(onReady, onExit)
}

func saveUserCfg() {
	if err := userconfig.Save(appName, userCfg); err != nil {
		log.Printf("save config: %v", err)
		notify("Save preferences failed", err.Error())
	}
}

func notify(title, body string) {
	if err := beeep.Notify(title, body, ""); err != nil {
		log.Printf("notify: %v", err)
	}
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

func onReady() {
	systray.SetIcon(wickIcon())
	systray.SetTitle(appName)
	systray.SetTooltip(appName + " — " + project)

	mProject := systray.AddMenuItem("Project: "+filepath.Base(project), project)
	mProject.Disable()
	systray.AddSeparator()

	mServer := systray.AddMenuItem("Start server", "Toggle HTTP server")
	mWorker := systray.AddMenuItem("Start worker", "Toggle background job worker")
	mLogs := systray.AddMenuItem("Open logs", "Open "+logPath)
	if logPath == "" {
		mLogs.Disable()
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
						notify("MCP install failed", ui.c.Label+": "+err.Error())
					} else {
						notify("MCP installed", appName+" → "+ui.c.Label)
						ui.refresh()
					}
				case <-uninstall.ClickedCh:
					if err := mcpconfig.Uninstall(ui.c, appName); err != nil {
						notify("MCP uninstall failed", ui.c.Label+": "+err.Error())
					} else {
						notify("MCP uninstalled", appName+" removed from "+ui.c.Label)
						ui.refresh()
					}
				case <-open.ClickedCh:
					if err := openInEditor(ui.c.Path); err != nil {
						notify("Open failed", ui.c.Path+": "+err.Error())
					}
				}
			}
		}(ui, install, uninstall, open)
	}
	systray.AddSeparator()

	mPrefs := systray.AddMenuItem("Preferences", "Per-machine settings (saved to "+cfgPath+")")
	mAutoSrv := mPrefs.AddSubMenuItemCheckbox("Auto-start server on launch", "Start HTTP server immediately when tray opens", userCfg.AutoStartServer)
	mAutoWrk := mPrefs.AddSubMenuItemCheckbox("Auto-start worker on launch", "Start background worker immediately when tray opens", userCfg.AutoStartWorker)
	mAutoUpd := mPrefs.AddSubMenuItemCheckbox("Auto-update", "Check + download new releases in background", userCfg.AutoUpdate)
	mPrefs.AddSubMenuItem("─────────────", "").Disable()
	mOpenCfg := mPrefs.AddSubMenuItem("Open config file", "Open "+cfgPath)
	if cfgPath == "" {
		mOpenCfg.Disable()
	}
	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit "+appName)

	setServerLabel := func(running bool) {
		if running {
			mServer.SetTitle(fmt.Sprintf("Stop server  (running on :%d)", serverPort))
		} else {
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
			notify("Server start failed", err.Error())
			setServerLabel(false)
		} else {
			setServerLabel(true)
			notify("Server started", fmt.Sprintf("HTTP server running on :%d", serverPort))
		}
	} else {
		setServerLabel(false)
	}
	if userCfg.AutoStartWorker {
		if err := startWorker(); err != nil {
			notify("Worker start failed", err.Error())
			setWorkerLabel(false)
		} else {
			setWorkerLabel(true)
			notify("Worker started", "Background worker running")
		}
	} else {
		setWorkerLabel(false)
	}

	go func() {
		for {
			select {
			case <-mServer.ClickedCh:
				if isServerRunning() {
					stopServer()
					setServerLabel(false)
					notify("Server stopped", "HTTP server stopped")
				} else if err := startServer(); err != nil {
					notify("Server start failed", err.Error())
				} else {
					setServerLabel(true)
					notify("Server started", fmt.Sprintf("HTTP server running on :%d", serverPort))
				}
			case <-mWorker.ClickedCh:
				if isWorkerRunning() {
					stopWorker()
					setWorkerLabel(false)
					notify("Worker stopped", "Background worker stopped")
				} else if err := startWorker(); err != nil {
					notify("Worker start failed", err.Error())
				} else {
					setWorkerLabel(true)
					notify("Worker started", "Background worker running")
				}
			case <-mLogs.ClickedCh:
				if logPath != "" {
					if err := openInEditor(logPath); err != nil {
						notify("Open failed", err.Error())
					}
				}
			case <-mInstallAll.ClickedCh:
				ok, fail := 0, 0
				for _, ui := range uis {
					if err := installOne(ui.c); err != nil {
						log.Printf("install %s: %v", ui.c.ID, err)
						fail++
					} else {
						ok++
						ui.refresh()
					}
				}
				notify("MCP install all", fmt.Sprintf("%d installed, %d failed", ok, fail))
			case <-mUninstallAll.ClickedCh:
				ok, fail := 0, 0
				for _, ui := range uis {
					if err := mcpconfig.Uninstall(ui.c, appName); err != nil {
						log.Printf("uninstall %s: %v", ui.c.ID, err)
						fail++
					} else {
						ok++
						ui.refresh()
					}
				}
				notify("MCP uninstall all", fmt.Sprintf("%d removed, %d failed", ok, fail))
			case <-mAutoSrv.ClickedCh:
				userCfg.AutoStartServer = !userCfg.AutoStartServer
				if userCfg.AutoStartServer {
					mAutoSrv.Check()
				} else {
					mAutoSrv.Uncheck()
				}
				saveUserCfg()
			case <-mAutoWrk.ClickedCh:
				userCfg.AutoStartWorker = !userCfg.AutoStartWorker
				if userCfg.AutoStartWorker {
					mAutoWrk.Check()
				} else {
					mAutoWrk.Uncheck()
				}
				saveUserCfg()
			case <-mAutoUpd.ClickedCh:
				userCfg.AutoUpdate = !userCfg.AutoUpdate
				if userCfg.AutoUpdate {
					mAutoUpd.Check()
				} else {
					mAutoUpd.Uncheck()
				}
				saveUserCfg()
			case <-mOpenCfg.ClickedCh:
				if cfgPath != "" {
					if err := openInEditor(cfgPath); err != nil {
						notify("Open failed", err.Error())
					}
				}
			case <-mExample.ClickedCh:
				path, err := writeExampleConfig()
				if err != nil {
					notify("Example failed", err.Error())
					continue
				}
				if err := openInEditor(path); err != nil {
					notify("Open failed", err.Error())
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
	ctx, cancel := context.WithCancel(context.Background())
	serverCancel = cancel
	serverDone = make(chan struct{})
	mu.Unlock()

	srv := api.NewServer()
	go func() {
		defer close(serverDone)
		if err := srv.Run(ctx, serverPort); err != nil {
			log.Printf("server: %v", err)
			notify("Server crashed", err.Error())
		}
		mu.Lock()
		serverCancel = nil
		mu.Unlock()
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
	workerCancel = cancel
	workerDone = make(chan struct{})
	mu.Unlock()

	srv := worker.NewServer()
	go func() {
		defer close(workerDone)
		if err := srv.Run(ctx); err != nil {
			log.Printf("worker: %v", err)
			notify("Worker crashed", err.Error())
		}
		mu.Lock()
		workerCancel = nil
		mu.Unlock()
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
	out, err := jsonIndent(snippet)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(os.TempDir(), appName+"-mcp-config.json")
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

