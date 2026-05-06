//go:build !headless

package systemtray

import (
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	logPrefix       = "wick-"
	logSuffix       = ".log"
	dateLayout      = "2006-01-02"
	defaultRetentionDays = 7
)

// setupLogFile redirects zerolog output to <UserConfigDir>/<appName>/logs/wick-YYYY-MM-DD.log
// (tee'd with stderr) and prunes per-day files older than retentionDays.
// Caller defers the returned cleanup func.
//
// Co-located with config.json under UserConfigDir so everything an app
// owns lives in one tree per OS:
//
//	Windows: %APPDATA%\<appName>\logs\wick-YYYY-MM-DD.log
//	macOS  : ~/Library/Application Support/<appName>/logs/wick-YYYY-MM-DD.log
//	Linux  : ~/.config/<appName>/logs/wick-YYYY-MM-DD.log
//
// Server + worker goroutines that share this process write here. MCP
// serve subprocesses (spawned per request by clients like Claude /
// Cursor) get their own stderr; not tee'd into this file.
func setupLogFile(appName string, retentionDays int) (string, func(), error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", func() {}, err
	}
	dir = filepath.Join(dir, appName, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", func() {}, err
	}
	if retentionDays <= 0 {
		retentionDays = defaultRetentionDays
	}
	pruneOldLogs(dir, retentionDays)

	path := filepath.Join(dir, logPrefix+time.Now().Format(dateLayout)+logSuffix)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", func() {}, err
	}
	// Tee zerolog + stdlib log into the file alongside stderr.
	mw := io.MultiWriter(os.Stderr, f)
	log.Logger = zerolog.New(mw).With().Timestamp().Logger()
	stdlog.SetOutput(mw)

	// Pipe os.Stdout + os.Stderr through goroutines so any direct write
	// (fmt.Printf from app code, third-party libs, panic traces) lands
	// in the same log file. Without this, windowsgui builds drop those
	// writes silently because there's no real console attached.
	origOut, origErr := os.Stdout, os.Stderr
	if rOut, wOut, perr := os.Pipe(); perr == nil {
		os.Stdout = wOut
		go io.Copy(io.MultiWriter(origOut, f), rOut)
	}
	if rErr, wErr, perr := os.Pipe(); perr == nil {
		os.Stderr = wErr
		go io.Copy(io.MultiWriter(origErr, f), rErr)
	}

	return path, func() { f.Close() }, nil
}

// pruneOldLogs removes wick-YYYY-MM-DD.log files older than retentionDays.
// Best-effort: errors logged but not surfaced (we don't want startup
// to fail because the user's filesystem is weird).
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
		if !strings.HasPrefix(name, logPrefix) || !strings.HasSuffix(name, logSuffix) {
			continue
		}
		dateStr := strings.TrimSuffix(strings.TrimPrefix(name, logPrefix), logSuffix)
		t, err := time.Parse(dateLayout, dateStr)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
