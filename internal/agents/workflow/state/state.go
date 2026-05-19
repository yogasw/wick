// Package state persists per-run state.json + events.jsonl under
// `<BaseDir>/workflows/<id>/runs/<run-id>/`. Atomic writes via the
// shared internal/agents/storage helpers. In-memory variant available
// for tests.
package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/workflow"
)

// Store persists RunState + appends RunEvent for one workflow's
// runs/ folder.
type Store interface {
	Save(id, runID string, st workflow.RunState) error
	Load(id, runID string) (workflow.RunState, error)
	AppendEvent(id, runID string, ev workflow.RunEvent) error
	ListEvents(id, runID string) ([]workflow.RunEvent, error)
	ListRuns(id string) ([]string, error)
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

// ListEvents reads + decodes the full events.jsonl. Empty file → nil.
func (s *FileStore) ListEvents(id, runID string) ([]workflow.RunEvent, error) {
	data, err := os.ReadFile(s.Layout.WorkflowRunEvents(id, runID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []workflow.RunEvent{}
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var ev workflow.RunEvent
		if err := dec.Decode(&ev); err != nil {
			return nil, fmt.Errorf("events.jsonl decode: %w", err)
		}
		out = append(out, ev)
	}
	return out, nil
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
