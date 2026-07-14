package agents

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/tool"
)

// Helpers that resolve the runtime log files relevant to a spawn's detail
// page. The Logs viewer page itself was removed; these remain because the
// spawn detail still surfaces the on-disk log paths (spawn jsonl + the
// process logs from the same day) so an operator can copy them out for
// analysis, plus the spawn's time window to scan.
//
// The logs dir sits beside the agents base dir: globalLayout.BaseDir is
// `<app-dir>/agents`, so logs live at `<app-dir>/logs`.

// logsDir returns the absolute path to the runtime logs directory, or ""
// when the layout is not yet wired.
func logsDir() string {
	if globalLayout.BaseDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(globalLayout.BaseDir), "logs")
}

// spawnLogsDTO builds the log block for a spawn's detail page: the spawn's
// own jsonl path, every process log (app/server/worker/mcp/gate/daemon)
// written on the spawn's day(s), and the spawn's start→end window so the
// operator knows where to scan. endedAt is the zero time while the spawn
// is still running.
//
// Log files are dated by LOCAL time (logfiles.Setup + the daemon log both
// use time.Now() local), so we match on the local date of the spawn — and
// on both the start and end dates when a spawn straddles midnight.
// endedAt is the exit-event time (clean end); zero when there was no exit
// event. unclean=true means the process died without an exit (crash/kill) —
// endedAt then carries a best-effort "last sign of life" so the window still
// shows a range instead of a misleading "running".
func spawnLogsDTO(spawnPath string, startedAt, endedAt time.Time, unclean bool) SpawnLogsDTO {
	out := SpawnLogsDTO{SpawnPath: spawnPath}

	// Time window.
	out.Window = SpawnWindow{Start: startedAt.Format(time.RFC3339)}
	switch {
	case !endedAt.IsZero():
		// Real exit, or an approximate end for an unclean death.
		out.Window.End = endedAt.Format(time.RFC3339)
		out.Window.Unclean = unclean
		if d := endedAt.Sub(startedAt); d > 0 {
			out.Window.DurationMs = d.Milliseconds()
		}
	case unclean:
		// Died with no exit AND no later event to date the end — mark ended
		// (unclean) without a precise end rather than claiming "running".
		out.Window.Unclean = true
	default:
		out.Window.Running = true
	}

	dir := logsDir()
	if dir == "" {
		return out
	}
	out.LogsDir = dir

	// The set of local dates the spawn touched (start; and end if different).
	dates := map[string]bool{startedAt.Local().Format("2006-01-02"): true}
	if !endedAt.IsZero() {
		dates[endedAt.Local().Format("2006-01-02")] = true
	}

	// Collect every process log whose date is in the set. One dir read; the
	// spawn jsonl itself lives elsewhere so it never shows up here.
	// LogsPresent counts ALL .log files (any date) so the UI can tell
	// "none for this day" apart from "no log files at all" (dev/console mode,
	// where cmd/lab logs to stdout instead of logs/*.log).
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		out.LogsPresent++
		prefix, date := parseLogName(e.Name())
		if date == "" || !dates[date] {
			continue
		}
		out.Components = append(out.Components, LogRefDTO{
			Prefix: prefix,
			Path:   filepath.Join(dir, e.Name()),
		})
	}
	// Stable order: component name, then filename (so multi-day spawns list
	// each day's file in date order under its component).
	sort.Slice(out.Components, func(i, j int) bool {
		if out.Components[i].Prefix != out.Components[j].Prefix {
			return out.Components[i].Prefix < out.Components[j].Prefix
		}
		return out.Components[i].Path < out.Components[j].Path
	})
	return out
}

// LogTailResponse is the JSON envelope for GET /api/providers/logs/{file}.
type LogTailResponse struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"` // older bytes dropped to fit the byte cap
	Modified  string `json:"modified"`
}

const defaultLogTailBytes = 256 * 1024
const maxLogTailBytes = 4 * 1024 * 1024

// apiLogTail returns the last ~N bytes of one log file for the in-app log
// viewer. The viewer is opened from a spawn's Logs block so an operator can
// read server/mcp/worker/… output around the spawn window without shelling in.
func apiLogTail(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	path, name, ok := resolveLogFile(c)
	if !ok {
		return
	}
	n := int64(defaultLogTailBytes)
	if v := c.Query("bytes"); v != "" {
		if b, err := strconv.ParseInt(v, 10, 64); err == nil && b > 0 {
			n = b
		}
	}
	if n > maxLogTailBytes {
		n = maxLogTailBytes
	}
	f, err := os.Open(path)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	truncated := false
	if info.Size() > n {
		if _, err := f.Seek(info.Size()-n, io.SeekStart); err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
		truncated = true
	}
	data, err := io.ReadAll(f)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	// Drop a partial leading line when truncated so the view starts clean.
	if truncated {
		if i := strings.IndexByte(string(data), '\n'); i >= 0 && i+1 < len(data) {
			data = data[i+1:]
		}
	}
	c.JSON(http.StatusOK, LogTailResponse{
		Name:      name,
		Path:      path,
		Size:      info.Size(),
		Content:   string(data),
		Truncated: truncated,
		Modified:  info.ModTime().UTC().Format(time.RFC3339),
	})
}

// apiLogDownload streams the full log file as an attachment.
func apiLogDownload(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	path, name, ok := resolveLogFile(c)
	if !ok {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	c.W.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.W.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.W.WriteHeader(http.StatusOK)
	_, _ = io.Copy(c.W, f)
}

// resolveLogFile validates the {file} path param against traversal and
// resolves it to an absolute path inside the logs dir.
func resolveLogFile(c *tool.Ctx) (path, name string, ok bool) {
	name = c.PathValue("file")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || !strings.HasSuffix(name, ".log") {
		c.Error(http.StatusBadRequest, "invalid log filename")
		return "", "", false
	}
	dir := logsDir()
	if dir == "" {
		c.Error(http.StatusServiceUnavailable, "layout not wired")
		return "", "", false
	}
	full := filepath.Join(dir, name)
	if rel, err := filepath.Rel(dir, full); err != nil || strings.HasPrefix(rel, "..") {
		c.Error(http.StatusBadRequest, "invalid log path")
		return "", "", false
	}
	if _, err := os.Stat(full); err != nil {
		c.NotFound()
		return "", "", false
	}
	return full, name, true
}

// parseLogName splits <prefix>-YYYY-MM-DD.log into ("prefix","YYYY-MM-DD").
// A name that doesn't carry a trailing date returns ("<stem>",""). Mirrors
// the dated-file convention in internal/pkg/logfiles + the daemon log.
func parseLogName(name string) (prefix, date string) {
	stem := strings.TrimSuffix(name, ".log")
	parts := strings.Split(stem, "-")
	if len(parts) >= 4 {
		datePart := strings.Join(parts[len(parts)-3:], "-")
		if _, err := time.Parse("2006-01-02", datePart); err == nil {
			return strings.Join(parts[:len(parts)-3], "-"), datePart
		}
	}
	return stem, ""
}
