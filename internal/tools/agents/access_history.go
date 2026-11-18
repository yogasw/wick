package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/accessreq"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// Access request history lives in files, next to the conversation it
// belongs to, like its turns: one JSON snapshot per request under
// `sessions/<id>/access/<request_id>.json`, rewritten on every change.
// A session of a project also adds one line to the project's
// `projects/<id>/access/index.jsonl` the first time a request is seen,
// so the project's settings can list its history without walking every
// session. The database keeps only what is live (and the rules).

// AccessFilePublisher writes the history files.
type AccessFilePublisher struct {
	Layout agentconfig.Layout
	// Project names the project of a session ("" when none).
	Project func(sessionID string) string
}

var accessIndexMu sync.Mutex

func accessSessionDir(l agentconfig.Layout, sessionID string) string {
	return filepath.Join(l.SessionDir(sessionID), "access")
}

func accessIndexFile(l agentconfig.Layout, projectID string) string {
	return filepath.Join(l.ProjectDir(projectID), "access", "index.jsonl")
}

// accessIndexLine is one request in a project's index.
type accessIndexLine struct {
	RequestID string    `json:"request_id"`
	SessionID string    `json:"session_id"`
	At        time.Time `json:"at"`
}

// safeAccessID keeps a request or session id usable as a path part.
func safeAccessID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && id != "." && id != ".."
}

// Publish satisfies accessreq.Publisher.
func (p AccessFilePublisher) Publish(_ context.Context, v accessreq.View) (map[string]string, error) {
	if p.Layout == (agentconfig.Layout{}) || !safeAccessID(v.SessionID) || !safeAccessID(v.ID) {
		return nil, nil
	}
	dir := accessSessionDir(p.Layout, v.SessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, v.ID+".json")
	_, statErr := os.Stat(file)
	first := os.IsNotExist(statErr)
	b, err := json.Marshal(accessViewDTO(v))
	if err != nil {
		return nil, err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, file); err != nil {
		return nil, err
	}
	if first && p.Project != nil {
		if pid := p.Project(v.SessionID); safeAccessID(pid) {
			if err := appendAccessIndex(p.Layout, pid, accessIndexLine{RequestID: v.ID, SessionID: v.SessionID, At: v.CreatedAt}); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func appendAccessIndex(l agentconfig.Layout, projectID string, line accessIndexLine) error {
	file := accessIndexFile(l, projectID)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	accessIndexMu.Lock()
	defer accessIndexMu.Unlock()
	f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// projectAccessHistory reads the newest limit requests of a project from
// its index and the sessions' snapshot files. A session that was deleted
// takes its history with it, like its turns.
func projectAccessHistory(l agentconfig.Layout, projectID string, limit int) []map[string]any {
	out := []map[string]any{}
	if l == (agentconfig.Layout{}) || !safeAccessID(projectID) {
		return out
	}
	f, err := os.Open(accessIndexFile(l, projectID))
	if err != nil {
		return out
	}
	defer f.Close()
	var lines []accessIndexLine
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var ln accessIndexLine
		if json.Unmarshal(sc.Bytes(), &ln) != nil || !safeAccessID(ln.RequestID) || !safeAccessID(ln.SessionID) || seen[ln.RequestID] {
			continue
		}
		seen[ln.RequestID] = true
		lines = append(lines, ln)
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].At.After(lines[j].At) })
	for _, ln := range lines {
		if len(out) >= limit {
			break
		}
		b, err := os.ReadFile(filepath.Join(accessSessionDir(l, ln.SessionID), ln.RequestID+".json"))
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			m["session_id"] = ln.SessionID
			out = append(out, m)
		}
	}
	return out
}
