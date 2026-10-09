// Package logfiles wires per-component dated log files for wick
// runtimes that own their own process (tray on GUI hosts, headless
// `all` spawned by `wick start`). It opens app/server/worker/mcp
// files under ~/.<appName>/logs/, pipes os.Stdout / os.Stderr into
// app.log so fmt.Printf and panic traces survive when the real
// console is detached, and prunes files older than the retention
// window.
//
// Layout:
//
//	~/.<appName>/logs/
//	  app-YYYY-MM-DD.log     // tray / startup / global zerolog + stdout/stderr
//	  server-YYYY-MM-DD.log  // HTTP server (zerolog component=server)
//	  worker-YYYY-MM-DD.log  // job worker (zerolog component=worker)
//	  mcp-YYYY-MM-DD.log     // wickmanager MCP audit
//
// Each file is tee'd to the original stderr so an interactive operator
// still sees output in the terminal. When the caller has no real
// stderr (tray detaches the console, daemon redirects to daemon-DATE.log),
// the file is the only sink — that's the intent.
package logfiles

import (
	"bytes"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/userconfig"
)

const (
	logSuffix            = ".log"
	dateLayout           = "2006-01-02"
	defaultRetentionDays = 1

	// pruneInterval is how often the background pass re-applies the
	// retention window. Setup used to prune only at boot, so a daemon that
	// stays up for weeks never deleted anything.
	pruneInterval = time.Hour

	// maxUndatedBytes caps a log file that carries no date in its name
	// (daemon-stderr.log is opened by the supervisor, not by us, so it can
	// never roll). Past the cap the file is cut down to keepUndatedBytes.
	maxUndatedBytes  int64 = 50 << 20
	keepUndatedBytes int64 = 5 << 20
)

// dailyFile is an io.WriteCloser for <prefix>-YYYY-MM-DD.log that follows
// the local date: the first write after midnight closes yesterday's file and
// opens today's. Setup opened the file once at boot, so a long-lived daemon
// kept appending to the day it started — one ever-growing file the retention
// pass could not remove because its date was never old enough.
type dailyFile struct {
	mu     sync.Mutex
	dir    string
	prefix string
	date   string
	f      *os.File
}

func newDailyFile(dir, prefix string) (*dailyFile, error) {
	d := &dailyFile{dir: dir, prefix: prefix}
	if err := d.open(time.Now().Format(dateLayout)); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *dailyFile) open(date string) error {
	f, err := os.OpenFile(
		filepath.Join(d.dir, d.prefix+"-"+date+logSuffix),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644,
	)
	if err != nil {
		return err
	}
	if d.f != nil {
		d.f.Close()
	}
	d.f, d.date = f, date
	return nil
}

func (d *dailyFile) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if today := time.Now().Format(dateLayout); today != d.date {
		// A failed roll keeps writing to the old file rather than dropping
		// the line.
		_ = d.open(today)
	}
	return d.f.Write(p)
}

func (d *dailyFile) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.f.Close()
}

// Set bundles the per-component loggers produced by Setup. Callers
// wire each logger into the relevant subsystem (server / worker / mcp)
// and use App as the global zerolog default.
type Set struct {
	App    zerolog.Logger
	Server zerolog.Logger
	Worker zerolog.Logger
	MCP    zerolog.Logger
	Dir    string
}

// bestEffortWriter writes to primary; on success also attempts secondary
// (ignoring secondary errors). Ensures the file always receives the
// write even when stderr is unavailable (tray detaches the console).
type bestEffortWriter struct {
	primary   io.Writer
	secondary io.Writer
}

func (w *bestEffortWriter) Write(p []byte) (int, error) {
	n, err := w.primary.Write(p)
	if err == nil {
		w.secondary.Write(p) //nolint:errcheck
	}
	return n, err
}

// Setup creates the dated log files, installs stdout/stderr pipes that
// funnel fmt.Printf and panic traces into app.log, points the global
// zerolog log.Logger and stdlib log at the App logger, and returns a
// cleanup func that flushes the pipe goroutines then closes all files.
// retentionDays <= 0 falls back to the default (1 day).
func Setup(appName string, retentionDays int) (Set, func(), error) {
	dir, err := userconfig.Dir(appName)
	if err != nil {
		return Set{}, func() {}, err
	}
	dir = filepath.Join(dir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Set{}, func() {}, err
	}
	if retentionDays <= 0 {
		retentionDays = defaultRetentionDays
	}
	pruneOldLogs(dir, retentionDays)
	capUndatedLogs(dir, maxUndatedBytes, keepUndatedBytes)
	stopPrune := startPruner(dir, retentionDays)

	openLog := func(prefix string) (*dailyFile, error) {
		return newDailyFile(dir, prefix)
	}
	fApp, err := openLog("app")
	if err != nil {
		return Set{}, func() {}, err
	}
	fSrv, err := openLog("server")
	if err != nil {
		fApp.Close()
		return Set{}, func() {}, err
	}
	fWrk, err := openLog("worker")
	if err != nil {
		fApp.Close()
		fSrv.Close()
		return Set{}, func() {}, err
	}
	fMCP, err := openLog("mcp")
	if err != nil {
		fApp.Close()
		fSrv.Close()
		fWrk.Close()
		return Set{}, func() {}, err
	}

	origOut, origErr := os.Stdout, os.Stderr
	// Remember the real stdio so a forked successor can inherit IT rather
	// than our log pipes. See WithOriginalStdio.
	realStdout, realStderr = origOut, origErr

	var wg sync.WaitGroup
	var pipeWriters []*os.File

	if rOut, wOut, perr := os.Pipe(); perr == nil {
		os.Stdout = wOut
		pipeWriters = append(pipeWriters, wOut)
		wg.Add(1)
		go func() {
			defer wg.Done()
			io.Copy(&bestEffortWriter{primary: fApp, secondary: origOut}, rOut)
		}()
	}
	if rErr, wErr, perr := os.Pipe(); perr == nil {
		os.Stderr = wErr
		pipeWriters = append(pipeWriters, wErr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			io.Copy(&bestEffortWriter{primary: fApp, secondary: origErr}, rErr)
		}()
	}

	mwApp := &bestEffortWriter{primary: fApp, secondary: origErr}
	mwSrv := &bestEffortWriter{primary: fSrv, secondary: origErr}
	mwWrk := &bestEffortWriter{primary: fWrk, secondary: origErr}
	mwMCP := &bestEffortWriter{primary: fMCP, secondary: origErr}

	ls := Set{
		App:    zerolog.New(mwApp).With().Timestamp().Logger(),
		Server: zerolog.New(mwSrv).With().Timestamp().Logger(),
		Worker: zerolog.New(mwWrk).With().Timestamp().Logger(),
		MCP:    zerolog.New(mwMCP).With().Timestamp().Logger(),
		Dir:    dir,
	}

	log.Logger = ls.App
	zerolog.SetGlobalLevel(globalLevel())
	stdlog.SetOutput(mwApp)

	return ls, func() {
		stopPrune()
		for _, w := range pipeWriters {
			w.Close()
		}
		// Bounded wait, NOT wg.Wait().
		//
		// Each copier reads until its pipe hits EOF, which needs every writer
		// of that pipe closed. During a graceful upgrade the successor process
		// inherits this process's stdout/stderr — it holds the write ends
		// open for its whole life — so an unbounded wait here never returns:
		// the old process hangs after a clean drain, and because tableflip
		// refuses to upgrade while a parent is still alive, the NEXT upgrade
		// is refused too. Losing a couple of trailing log lines is the right
		// trade against a daemon that cannot be replaced.
		drained := make(chan struct{})
		go func() {
			wg.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(2 * time.Second):
		}
		fApp.Close()
		fSrv.Close()
		fWrk.Close()
		fMCP.Close()
	}, nil
}

// realStdout / realStderr are the process's stdio as it was handed to us,
// before Setup redirected os.Stdout / os.Stderr into the log pipes.
var realStdout, realStderr *os.File

// WithOriginalStdio runs fn with os.Stdout / os.Stderr temporarily restored to
// the process's real stdio.
//
// This exists for forking a successor during a graceful upgrade. The fork
// hands the child os.Stdout / os.Stderr as they are at that moment, and after
// Setup those are OUR log pipes — read by goroutines that die with this
// process. The child would inherit fd 1 and 2 pointing at pipes nobody reads,
// and Go makes SIGPIPE on fd 1/2 fatal: the successor was dying seconds after
// a perfectly clean handoff. It also meant the child's early output was
// copied into the parent's log file, so every boot line appeared twice.
//
// The swap is brief and only affects direct writers to os.Stdout/os.Stderr
// during the fork; the loggers write to their files either way.
func WithOriginalStdio(fn func() error) error {
	if realStdout == nil || realStderr == nil {
		return fn()
	}
	so, se := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = realStdout, realStderr
	defer func() { os.Stdout, os.Stderr = so, se }()
	return fn()
}

// globalLevel returns the zerolog level for the process: LOG_LEVEL when set
// to a level zerolog knows, otherwise info. Debug used to be on by default,
// which wrote one line per streamed token of every agent session — gigabytes
// a day into daemon-stderr.log.
func globalLevel() zerolog.Level {
	if v := strings.TrimSpace(os.Getenv("LOG_LEVEL")); v != "" {
		if lvl, err := zerolog.ParseLevel(strings.ToLower(v)); err == nil {
			return lvl
		}
	}
	return zerolog.InfoLevel
}

// startPruner re-applies retention and the undated-file cap every
// pruneInterval until the returned stop func is called.
func startPruner(dir string, retentionDays int) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(pruneInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				pruneOldLogs(dir, retentionDays)
				capUndatedLogs(dir, maxUndatedBytes, keepUndatedBytes)
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// capUndatedLogs trims every *.log in dir whose name carries no
// YYYY-MM-DD date and that grew past max, keeping only its last keep bytes.
// The supervisor holds such a file open with O_APPEND, so truncating in
// place frees the space without a restart and the writer carries on at the
// new end.
func capUndatedLogs(dir string, max, keep int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, logSuffix) || hasLogDate(name) {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.Size() <= max {
			continue
		}
		trimToTail(path, keep)
	}
}

func hasLogDate(name string) bool {
	parts := strings.Split(strings.TrimSuffix(name, logSuffix), "-")
	if len(parts) < 3 {
		return false
	}
	_, err := time.Parse(dateLayout, strings.Join(parts[len(parts)-3:], "-"))
	return err == nil
}

// trimToTail rewrites path to hold only its last keep bytes.
func trimToTail(path string, keep int64) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() <= keep {
		return
	}
	tail := make([]byte, keep)
	if _, err := f.ReadAt(tail, info.Size()-keep); err != nil {
		return
	}
	// Drop the partial first line so the file still starts on a record.
	if i := bytes.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
		tail = tail[i+1:]
	}
	if err := f.Truncate(0); err != nil {
		return
	}
	_, _ = f.WriteAt(tail, 0)
}

// pruneOldLogs removes <prefix>-YYYY-MM-DD.log files older than retentionDays.
func pruneOldLogs(dir string, retentionDays int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, logSuffix) {
			continue
		}
		// Filename is <prefix>-YYYY-MM-DD.log. The date itself contains
		// dashes, so take the LAST THREE dash-separated segments as the
		// date — not everything after the last dash (which is just the
		// day and never parses as a full date).
		base := strings.TrimSuffix(name, logSuffix)
		parts := strings.Split(base, "-")
		if len(parts) < 3 {
			continue
		}
		datePart := strings.Join(parts[len(parts)-3:], "-")
		t, err := time.Parse(dateLayout, datePart)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
