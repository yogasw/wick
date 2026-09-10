// Package state persists per-run state.json + events.jsonl under
// `<BaseDir>/workflows/<id>/runs/<run-id>/`. Atomic writes via the
// shared internal/agents/storage helpers. In-memory variant available
// for tests.
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/workflow"
)

// maxEventLine bounds one events.jsonl line so a malformed file can't
// exhaust memory. Matches the shardedlog scanner cap.
const maxEventLine = 1 << 20

// Store persists RunState + appends RunEvent for one workflow's
// runs/ folder.
type Store interface {
	Save(id, runID string, st workflow.RunState) error
	Load(id, runID string) (workflow.RunState, error)
	AppendEvent(id, runID string, ev workflow.RunEvent) error
	ListEvents(id, runID string) ([]workflow.RunEvent, error)
	ListEventsTail(id, runID string, limit int) ([]workflow.RunEvent, int, error)
	ListRuns(id string) ([]string, error)
	Delete(id, runID string) error
	IndexAppend(id string, entry IndexEntry) error
	IndexList(id string, page, pageSize int) ([]IndexEntry, bool, error)
}

// FileStore writes state.json + events.jsonl per run.
type FileStore struct {
	Layout config.Layout
}

// New returns the on-disk implementation.
func New(layout config.Layout) *FileStore {
	return &FileStore{Layout: layout}
}

// Save writes state.json atomically.
func (s *FileStore) Save(id, runID string, st workflow.RunState) error {
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = time.Now().UTC()
	}
	dir := s.Layout.WorkflowRunDir(id, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return storage.WriteJSON(s.Layout.WorkflowRunState(id, runID), st)
}

// Load reads state.json.
func (s *FileStore) Load(id, runID string) (workflow.RunState, error) {
	var st workflow.RunState
	if err := storage.ReadJSON(s.Layout.WorkflowRunState(id, runID), &st); err != nil {
		return workflow.RunState{}, err
	}
	return st, nil
}

// AppendEvent appends one line to events.jsonl atomically.
func (s *FileStore) AppendEvent(id, runID string, ev workflow.RunEvent) error {
	dir := s.Layout.WorkflowRunDir(id, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(s.Layout.WorkflowRunEvents(id, runID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// ListEvents streams the full events.jsonl. Absent file → nil; corrupt
// lines are skipped so one bad row never blanks the history.
func (s *FileStore) ListEvents(id, runID string) ([]workflow.RunEvent, error) {
	out := []workflow.RunEvent{}
	err := s.scanEvents(id, runID, func(ev workflow.RunEvent) {
		out = append(out, ev)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListEventsTail returns the most recent `limit` events (chronological)
// plus the total valid count. limit <= 0 returns all; memory is O(limit).
func (s *FileStore) ListEventsTail(id, runID string, limit int) ([]workflow.RunEvent, int, error) {
	if limit <= 0 {
		all, err := s.ListEvents(id, runID)
		if err != nil {
			return nil, 0, err
		}
		return all, len(all), nil
	}
	ring := make([]workflow.RunEvent, 0, limit)
	total := 0
	err := s.scanEvents(id, runID, func(ev workflow.RunEvent) {
		total++
		if len(ring) < limit {
			ring = append(ring, ev)
			return
		}
		copy(ring, ring[1:])
		ring[limit-1] = ev
	})
	if err != nil {
		return nil, 0, err
	}
	return ring, total, nil
}

// scanEvents walks events.jsonl line by line, calling fn per decoded
// event. Absent file is a no-op (a run with no events is valid).
func (s *FileStore) scanEvents(id, runID string, fn func(workflow.RunEvent)) error {
	f, err := os.Open(s.Layout.WorkflowRunEvents(id, runID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), maxEventLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev workflow.RunEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		fn(ev)
	}
	return sc.Err()
}

// ListRuns returns runs/<id> names sorted, newest first.
func (s *FileStore) ListRuns(id string) ([]string, error) {
	names, err := storage.ScanDirNames(s.Layout.WorkflowRunsDir(id))
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return names, nil
}

// Delete removes a run's on-disk folder (state.json + events.jsonl)
// and drops its row from the sharded index.
func (s *FileStore) Delete(id, runID string) error {
	if err := os.RemoveAll(s.Layout.WorkflowRunDir(id, runID)); err != nil {
		return err
	}
	_, err := s.indexStore(id).Remove(func(e IndexEntry) bool { return e.ID == runID })
	return err
}

// DeleteAll clears a workflow's whole run history — every
// runs/<runID>/ folder plus every row in the sharded index. Returns the
// number of run folders removed.
//
// Deliberately NOT a RemoveAll of the runs/ tree: the index lives
// *inside* runs/ (Layout.WorkflowIndexDir), and its cached
// shardedlog.Store carries the mutex that serialises appends. Dropping
// the directory under a run that fires mid-clear would race that
// writer, so the index is emptied through the Store instead.
//
// Best-effort per run: one undeletable folder doesn't abort the rest,
// and the first error is returned once everything else has been tried.
func (s *FileStore) DeleteAll(id string) (int, error) {
	names, err := s.ListRuns(id)
	if err != nil {
		return 0, err
	}
	// ListRuns scans runs/, which also contains the index dir.
	indexName := filepath.Base(s.Layout.WorkflowIndexDir(id))
	deleted := 0
	var firstErr error
	for _, name := range names {
		if name == indexName {
			continue
		}
		if err := os.RemoveAll(s.Layout.WorkflowRunDir(id, name)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		deleted++
	}
	if _, err := s.indexStore(id).Remove(func(IndexEntry) bool { return true }); err != nil && firstErr == nil {
		firstErr = err
	}
	return deleted, firstErr
}
