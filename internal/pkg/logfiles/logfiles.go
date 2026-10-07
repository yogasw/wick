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
	defaultRetentionDays = 7
)

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
// retentionDays <= 0 falls back to the default (7 days).
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

	date := time.Now().Format(dateLayout)
	openLog := func(prefix string) (*os.File, error) {
		return os.OpenFile(
			filepath.Join(dir, prefix+"-"+date+logSuffix),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644,
		)
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
	stdlog.SetOutput(mwApp)

	return ls, func() {
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
