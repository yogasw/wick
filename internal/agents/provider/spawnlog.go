package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SpawnLogger writes one jsonl file per spawn under
// `<base>/providers/spawns/`. Filename encodes provider type + name +
// session id + start unix-ts so an `ls` already filters cheaply by any
// of those without opening files. Example:
//
//	claude__work__abc123__1715234567890.jsonl
//
// Each file holds line-delimited JSON events for that single spawn:
// `start`, optional `version`, `error`, `exit`. The Providers UI lists
// recent files (newest first) and renders one event timeline per file.
type SpawnLogger struct {
	BaseDir string // <agents-base>/providers/spawns

	pruneMu sync.Mutex // serializes Prune so concurrent spawns don't race
}

// MaxSpawnLogs is how many spawn log files are retained. The newest N
// are kept; older ones are deleted on every new spawn. Keeps the
// Recent Spawns list bounded + the spawns/ dir from growing unbounded.
const MaxSpawnLogs = 50

// NewSpawnLogger returns a logger rooted at <agentsBase>/providers/spawns.
// agentsBase is typically Layout.BaseDir.
func NewSpawnLogger(agentsBase string) *SpawnLogger {
	return &SpawnLogger{
		BaseDir: filepath.Join(agentsBase, "providers", "spawns"),
	}
}

// Path returns the on-disk path for a spawn log without creating the
// file. Useful for tests.
func (s *SpawnLogger) Path(providerType, providerName, sessionID string, startedAt time.Time) string {
	name := fmt.Sprintf(
		"%s__%s__%s__%d.jsonl",
		safe(providerType),
		safe(providerName),
		safe(sessionID),
		startedAt.UnixMilli(),
	)
	return filepath.Join(s.BaseDir, name)
}

// SpawnEvent is one line in a spawn log file. Type carries the event
// kind (`start` / `version` / `error` / `exit`), other fields are
// populated based on Type — tests should match by Type rather than
// asserting on every field.
type SpawnEvent struct {
	Type         string    `json:"type"`
	At           time.Time `json:"at"`
	ProviderType string    `json:"provider_type,omitempty"`
	ProviderName string    `json:"provider_name,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	AgentName   string    `json:"agent_name,omitempty"`
	Workspace   string    `json:"workspace,omitempty"`
	ResumeID    string    `json:"resume_id,omitempty"`
	Binary      string    `json:"binary,omitempty"`
	Args        []string  `json:"args,omitempty"`
	Env         []string  `json:"env,omitempty"`
	// PID is the OS pid of the started subprocess. Set on the `start`
	// event after Spawner.Spawn returns; carried on `exit` so listings
	// can verify the same pid was reaped. 0 = test fake or unknown.
	PID int `json:"pid,omitempty"`
	// Origin is the session origin that triggered the spawn (e.g. "slack",
	// "telegram", "rest", "ui"). Written once on the initial start event
	// so the Recent Spawns list can show the channel without a session
	// registry lookup.
	Origin string `json:"origin,omitempty"`
	// FirstUserMessage is a short prefix of the user input that
	// triggered the spawn (truncated). Surfaces in the Backends UI
	// "Recent Spawns" list so operators see what each spawn was for.
	FirstUserMessage string    `json:"first_user_message,omitempty"`
	ExitReason       string    `json:"exit_reason,omitempty"`
	DurationMs       int64     `json:"duration_ms,omitempty"`
	Error            string    `json:"error,omitempty"`
	Message          string    `json:"message,omitempty"`
	// ReasonDetail is a human sentence on the exit event explaining WHY
	// the process ended — e.g. "idle TTL expired", "Stop() called
	// (preempt or session change)", "subprocess crashed: <stderr line>".
	// Populated from provider.ExitDetail so Recent Spawns shows a reason,
	// not just a one-word status.
	ReasonDetail string `json:"reason_detail,omitempty"`
	// ExitCode is the OS exit code on an abnormal (crash) exit; 0 for
	// clean / idle / stopped / respawn exits.
	ExitCode int `json:"exit_code,omitempty"`
	// StderrTail is the tail of the subprocess stderr on a crash — the
	// actual error message (bad model id, config.toml error, missing
	// binary). Empty when the process exited without writing stderr.
	StderrTail string `json:"stderr_tail,omitempty"`
}

// Append writes one event to the spawn log file, creating it on first
// call. Errors are returned but never panic — the caller (pool) treats
// them as logging failures, not spawn failures.
func (s *SpawnLogger) Append(path string, ev SpawnEvent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	encErr := enc.Encode(ev)
	_ = f.Close()
	// A `start` event marks a fresh spawn file — prune old ones so the
	// spawns/ dir stays bounded at MaxSpawnLogs. Best-effort.
	if ev.Type == "start" {
		_ = s.Prune(MaxSpawnLogs)
	}
	return encErr
}

// Prune keeps the newest `keep` spawn log files and deletes the rest.
// Serialized so concurrent spawns don't double-delete. Best-effort:
// individual delete errors are ignored (a missing file is already gone).
func (s *SpawnLogger) Prune(keep int) error {
	if keep < 0 {
		return nil
	}
	s.pruneMu.Lock()
	defer s.pruneMu.Unlock()

	entries, err := os.ReadDir(s.BaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	type fileTime struct {
		path string
		ts   int64
	}
	files := make([]fileTime, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		f, ok := parseSpawnLogName(e.Name())
		if !ok {
			continue
		}
		files = append(files, fileTime{filepath.Join(s.BaseDir, e.Name()), f.StartedAt.UnixMilli()})
	}
	if len(files) <= keep {
		return nil
	}
	// Newest first; delete everything past `keep`.
	sort.Slice(files, func(i, j int) bool { return files[i].ts > files[j].ts })
	for _, f := range files[keep:] {
		_ = os.Remove(f.path)
	}
	return nil
}

// SpawnLogFile is a parsed metadata view of one spawn log filename —
// used by the Providers page to filter by `ls` alone. PID +
// FirstUserMessage + ExitReason + Binary + Argv are populated by List
// from the file's first/last events (one read per file, cheap because
// spawn logs are short).
type SpawnLogFile struct {
	Path             string
	ProviderType     string
	ProviderName     string
	SessionID        string
	StartedAt        time.Time
	PID              int
	Origin           string // session origin (slack/telegram/rest/ui/…)
	FirstUserMessage string
	Binary           string
	Argv             []string
	Env              []string
	// ExitReason is "" while the spawn is still alive (no exit event
	// recorded yet), else "clean" / "idle" / "stopped" / "error".
	ExitReason string
	// ReasonDetail is the human "why it ended" sentence from the exit
	// event; "" while alive. Lets the Recent Spawns list show a reason
	// tooltip without opening the file.
	ReasonDetail string
	// ExitCode is the crash exit code from the exit event; 0 otherwise.
	ExitCode int
	// StderrTail is the crash stderr tail from the exit event; "" when
	// the process exited without writing stderr.
	StderrTail string
}

// FirstMessageWordLimit caps the spawn log's first_user_message at
// the first N whitespace-separated tokens. Word-based (not byte-based)
// so the preview reads naturally regardless of language; the UI table
// stays one line per row.
const FirstMessageWordLimit = 10

// TruncateFirstMessage keeps the first FirstMessageWordLimit words of
// text and appends "…" when more content was dropped. Whitespace
// inside the message is collapsed so multi-line input renders on one
// line.
func TruncateFirstMessage(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) <= FirstMessageWordLimit {
		return strings.Join(fields, " ")
	}
	return strings.Join(fields[:FirstMessageWordLimit], " ") + "…"
}

// List returns parsed metadata for every spawn log file under BaseDir,
// newest first. Filter args narrow the result; pass empty strings for
// wildcards. Files whose names don't match the canonical
// `<type>__<name>__<session>__<unix-ms>.jsonl` shape are skipped.
func (s *SpawnLogger) List(providerType, providerName, sessionID string) ([]SpawnLogFile, error) {
	entries, err := os.ReadDir(s.BaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]SpawnLogFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		f, ok := parseSpawnLogName(e.Name())
		if !ok {
			continue
		}
		if providerType != "" && f.ProviderType != providerType {
			continue
		}
		if providerName != "" && f.ProviderName != providerName {
			continue
		}
		if sessionID != "" && f.SessionID != sessionID {
			continue
		}
		f.Path = filepath.Join(s.BaseDir, e.Name())
		s.enrichFromEvents(&f)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

// Read parses every event line from one spawn log file in order.
func (s *SpawnLogger) Read(path string) ([]SpawnEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]SpawnEvent, 0)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev SpawnEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

// enrichFromEvents fills PID + FirstUserMessage from the `start` line
// and ExitReason from the most recent `exit` line, if present. Cheap
// even on cold disks: spawn log files are typically <10 lines.
func (s *SpawnLogger) enrichFromEvents(f *SpawnLogFile) {
	events, err := s.Read(f.Path)
	if err != nil {
		return
	}
	for _, ev := range events {
		switch ev.Type {
		case "start":
			if ev.PID != 0 {
				f.PID = ev.PID
			}
			if ev.Origin != "" && f.Origin == "" {
				f.Origin = ev.Origin
			}
			if ev.FirstUserMessage != "" {
				f.FirstUserMessage = ev.FirstUserMessage
			}
			if ev.Binary != "" {
				f.Binary = ev.Binary
			}
			if len(ev.Args) > 0 {
				f.Argv = ev.Args
			}
			if len(ev.Env) > 0 {
				f.Env = ev.Env
			}
		case "exit":
			f.ExitReason = ev.ExitReason
			f.ReasonDetail = ev.ReasonDetail
			f.ExitCode = ev.ExitCode
			f.StderrTail = ev.StderrTail
		}
	}
}

func parseSpawnLogName(name string) (SpawnLogFile, bool) {
	if !strings.HasSuffix(name, ".jsonl") {
		return SpawnLogFile{}, false
	}
	stem := strings.TrimSuffix(name, ".jsonl")
	parts := strings.Split(stem, "__")
	if len(parts) != 4 {
		return SpawnLogFile{}, false
	}
	var ms int64
	if _, err := fmt.Sscanf(parts[3], "%d", &ms); err != nil {
		return SpawnLogFile{}, false
	}
	return SpawnLogFile{
		ProviderType: parts[0],
		ProviderName: parts[1],
		SessionID:    parts[2],
		StartedAt:    time.UnixMilli(ms).UTC(),
	}, true
}

// safe replaces filename-hostile characters in a path component so the
// resulting filename is portable across Windows + POSIX.
func safe(s string) string {
	if s == "" {
		return "_"
	}
	repl := func(r rune) rune {
		switch r {
		case '_', '-', '.':
			return r
		}
		switch {
		case r >= '0' && r <= '9':
			return r
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		}
		return '-'
	}
	return strings.Map(repl, s)
}
