package systemtray

import (
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// setupLogFile redirects zerolog output to <UserCacheDir>/<appName>/wick.log
// (tee'd with stderr) and returns the absolute log path. Caller defers
// the returned cleanup func.
//
// Cache dir per OS:
//
//	Windows: %LOCALAPPDATA%\<appName>\wick.log
//	macOS  : ~/Library/Caches/<appName>/wick.log
//	Linux  : ~/.cache/<appName>/wick.log
func setupLogFile(appName string) (string, func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", func() {}, err
	}
	dir = filepath.Join(dir, appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", func() {}, err
	}
	path := filepath.Join(dir, "wick.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", func() {}, err
	}
	log.Logger = zerolog.New(io.MultiWriter(os.Stderr, f)).With().Timestamp().Logger()
	return path, func() { f.Close() }, nil
}
