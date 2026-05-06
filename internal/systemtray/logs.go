//go:build !headless

package systemtray

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
)

const (
	logSuffix            = ".log"
	dateLayout           = "2006-01-02"
	defaultRetentionDays = 7
)

// logSet holds per-component loggers and the log directory path.
type logSet struct {
	App    zerolog.Logger
	Server zerolog.Logger
	Worker zerolog.Logger
	Dir    string
}

// setupLogFiles creates three dated log files under
// <UserConfigDir>/<appName>/logs/:
//
//	app-YYYY-MM-DD.log    — tray / startup / app-level events
//	server-YYYY-MM-DD.log — HTTP server events
//	worker-YYYY-MM-DD.log — background job worker events
//
// Each file is also tee'd to the original stderr. The global zerolog
// log.Logger and stdlib log are set to the App logger. Stdout/Stderr are
// piped so fmt.Printf and panic traces land in app.log on windowsgui
// builds where there is no real console. Caller defers the returned
// cleanup func which flushes the pipe goroutines then closes all files.
func setupLogFiles(appName string, retentionDays int) (logSet, func(), error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return logSet{}, func() {}, err
	}
	dir = filepath.Join(dir, appName, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return logSet{}, func() {}, err
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
		return logSet{}, func() {}, err
	}
	fSrv, err := openLog("server")
	if err != nil {
		fApp.Close()
		return logSet{}, func() {}, err
	}
	fWrk, err := openLog("worker")
	if err != nil {
		fApp.Close()
		fSrv.Close()
		return logSet{}, func() {}, err
	}

	origOut, origErr := os.Stdout, os.Stderr

	var wg sync.WaitGroup
	var pipeWriters []*os.File

	// Pipe os.Stdout so fmt.Printf and panic traces land in app.log.
	if rOut, wOut, perr := os.Pipe(); perr == nil {
		os.Stdout = wOut
		pipeWriters = append(pipeWriters, wOut)
		wg.Add(1)
		go func() {
			defer wg.Done()
			io.Copy(io.MultiWriter(origOut, fApp), rOut)
		}()
	}
	// Pipe os.Stderr for windowsgui builds that have no real console.
	if rErr, wErr, perr := os.Pipe(); perr == nil {
		os.Stderr = wErr
		pipeWriters = append(pipeWriters, wErr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			io.Copy(io.MultiWriter(origErr, fApp), rErr)
		}()
	}

	mwApp := io.MultiWriter(origErr, fApp)
	mwSrv := io.MultiWriter(origErr, fSrv)
	mwWrk := io.MultiWriter(origErr, fWrk)

	ls := logSet{
		App:    zerolog.New(mwApp).With().Timestamp().Logger(),
		Server: zerolog.New(mwSrv).With().Timestamp().Logger(),
		Worker: zerolog.New(mwWrk).With().Timestamp().Logger(),
		Dir:    dir,
	}

	log.Logger = ls.App
	stdlog.SetOutput(mwApp)

	return ls, func() {
		for _, w := range pipeWriters {
			w.Close()
		}
		wg.Wait()
		fApp.Close()
		fSrv.Close()
		fWrk.Close()
	}, nil
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
		// Extract date from app-YYYY-MM-DD.log or legacy wick-YYYY-MM-DD.log
		base := strings.TrimSuffix(name, logSuffix)
		idx := strings.LastIndex(base, "-")
		if idx < 0 {
			continue
		}
		t, err := time.Parse(dateLayout, base[idx+1:])
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
