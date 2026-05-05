//go:build !headless

package systemtray

import (
	"io"
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

// setupLogFile redirects zerolog output to <UserCacheDir>/<appName>/wick-YYYY-MM-DD.log
// (tee'd with stderr) and prunes per-day files older than retentionDays.
// Caller defers the returned cleanup func.
//
// Cache dir per OS:
//
//	Windows: %LOCALAPPDATA%\<appName>\wick-YYYY-MM-DD.log
//	macOS  : ~/Library/Caches/<appName>/wick-YYYY-MM-DD.log
//	Linux  : ~/.cache/<appName>/wick-YYYY-MM-DD.log
//
// Server + worker goroutines that share this process write here. MCP
// serve subprocesses (spawned per request by clients like Claude /
// Cursor) get their own stderr; not tee'd into this file.
func setupLogFile(appName string, retentionDays int) (string, func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", func() {}, err
	}
	dir = filepath.Join(dir, appName)
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
	log.Logger = zerolog.New(io.MultiWriter(os.Stderr, f)).With().Timestamp().Logger()
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
