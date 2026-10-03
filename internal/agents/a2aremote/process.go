package a2aremote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/remote"
)

// Runtime is what one remote agent session needs: its settings, the auth
// in the clear (held only in memory for the life of the spawn) and the
// host policy.
type Runtime struct {
	Config Config
	Auth   PlainAuth
	Guard  Guard
}

// Spawner is a provider.Spawner with no process: Spawn returns a Process
// whose turns are A2A calls, run by the generic remote runner over Source.
type Spawner struct{ Runtime Runtime }

// Spawn starts the turn loop.
func (s Spawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	return remote.Spawner{Source: NewSource(s.Runtime)}.Spawn(ctx, opt)
}

// pollInterval spaces GetTask calls while a non-streaming task works.
var pollInterval = time.Second

// stateFile holds the session's A2A conversation state in its session dir.
const stateFile = "a2a-remote.json"

// State is a session's A2A conversation: one wick session = one contextId.
// TaskID is kept while the remote waits for input, so the next message
// continues that task.
type State struct {
	ContextID     string    `json:"context_id,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	InputRequired bool      `json:"input_required,omitempty"`
	LastState     string    `json:"last_state,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// LoadState reads dir's state; zero when there is none.
func LoadState(dir string) State {
	var st State
	if dir == "" {
		return st
	}
	if b, err := os.ReadFile(filepath.Join(dir, stateFile)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveState(dir string, st State) {
	if dir == "" {
		return
	}
	st.UpdatedAt = time.Now().UTC()
	b, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(dir, stateFile), b, 0o600); err != nil {
		log.Warn().Err(err).Msg("a2aremote: save session state")
	}
}

// run is one A2A call: the result is streamed when the card says it can
// stream, else sent with message/send and polled until it settles. Its
// events go to out, closed after the terminal one.
func (s *Source) run(ctx context.Context, dir, text string, out chan<- remote.Event) {
	defer close(out)
	cfg := s.rt.Config
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st := LoadState(dir)

	t := &turnState{st: &st, max: cfg.MaxBytes(), cancel: cancel, out: out}
	err := s.call(ctx, t, text)
	// The state is saved before the closing event, so whoever reads the
	// turn's end also reads where the conversation stands.
	var end remote.Event
	switch {
	case t.tooBig:
		end = t.finishError(fmt.Sprintf("The remote agent's reply passed the %d byte limit and was cut off.", t.max))
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		end = t.finishError(remote.TimeoutMessage(cfg.Timeout()))
	case err != nil:
		if errors.Is(ctx.Err(), context.Canceled) && !t.tooBig {
			return // the session was killed
		}
		end = t.finishError("A2A call failed: " + err.Error())
	default:
		end = t.finish()
	}
	saveState(dir, st)
	out <- end
}

func (s *Source) call(ctx context.Context, t *turnState, text string) error {
	card, err := s.rt.Config.ParsedCard()
	if err != nil {
		return err
	}
	client, err := NewClient(ctx, s.rt.Guard, card, s.rt.Auth, t.max)
	if err != nil {
		return err
	}
	defer func() { _ = client.Destroy() }()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(text))
	msg.ContextID = t.st.ContextID
	if t.st.InputRequired && t.st.TaskID != "" {
		msg.TaskID = a2a.TaskID(t.st.TaskID)
	}
	req := &a2a.SendMessageRequest{Message: msg}
	if card.Capabilities.Streaming {
		for ev, err := range client.SendStreamingMessage(ctx, req) {
			if err != nil {
				return err
			}
			t.event(ev)
			if t.tooBig {
				return ErrTooLarge
			}
		}
	} else {
		out, err := client.SendMessage(ctx, req)
		if err != nil {
			return err
		}
		t.event(out)
	}
	// A task that is still working (a non-streaming send that returned
	// early, or a stream that closed mid-task) is polled until it settles.
	for t.taskID != "" && !t.settled() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
		task, err := client.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(t.taskID)})
		if err != nil {
			return err
		}
		t.event(task)
		if t.tooBig {
			return ErrTooLarge
		}
	}
	return nil
}

// turnState follows one turn's events.
type turnState struct {
	out     chan<- remote.Event
	st      *State
	max     int64
	cancel  context.CancelFunc
	taskID  string
	state   a2a.TaskState
	message bool
	status  string // last status message text
	text    strings.Builder
	sent    int64
	tooBig  bool
	// artifacts maps an artifact id to the bytes of it already shown, so
	// a polled task that repeats its artifacts only adds what is new.
	artifacts map[a2a.ArtifactID]int
}

func (t *turnState) settled() bool {
	return t.message || t.state.Terminal() || t.state == a2a.TaskStateInputRequired || t.state == a2a.TaskStateAuthRequired
}

// write streams text to the chat, enforcing the size cap.
func (t *turnState) write(s string) {
	if s == "" || t.tooBig {
		return
	}
	if t.sent+int64(len(s)) > t.max {
		t.tooBig = true
		t.cancel()
		return
	}
	t.sent += int64(len(s))
	t.text.WriteString(s)
	t.out <- remote.Event{Kind: remote.EventTextDelta, Text: s}
}

func (t *turnState) context(id string) {
	if id != "" {
		t.st.ContextID = id
	}
}

func (t *turnState) artifact(a *a2a.Artifact, appendTo bool) {
	if a == nil {
		return
	}
	if t.artifacts == nil {
		t.artifacts = map[a2a.ArtifactID]int{}
	}
	text := partsText(a.Parts)
	if appendTo {
		t.artifacts[a.ID] += len(text)
		t.write(text)
		return
	}
	// A full artifact: show only the part not shown yet.
	seen := t.artifacts[a.ID]
	if seen < len(text) {
		t.artifacts[a.ID] = len(text)
		t.write(text[seen:])
	}
}

func (t *turnState) event(ev any) {
	switch v := ev.(type) {
	case *a2a.Message:
		t.context(v.ContextID)
		t.message = true
		t.write(MessageText(v))
	case *a2a.Task:
		t.context(v.ContextID)
		t.taskID = string(v.ID)
		for _, a := range v.Artifacts {
			t.artifact(a, false)
		}
		t.setStatus(v.Status)
	case *a2a.TaskStatusUpdateEvent:
		t.context(v.ContextID)
		t.taskID = string(v.TaskID)
		t.setStatus(v.Status)
	case *a2a.TaskArtifactUpdateEvent:
		t.context(v.ContextID)
		t.taskID = string(v.TaskID)
		t.artifact(v.Artifact, true)
	}
}

func (t *turnState) setStatus(s a2a.TaskStatus) {
	t.state = s.State
	if txt := MessageText(s.Message); txt != "" {
		t.status = txt
	}
}

// finish ends a turn that returned: the question of an input-required
// task is the reply and its task is kept for the next message; a failed
// task is an error; anything else completes with what was streamed, or
// the final status message when nothing was.
func (t *turnState) finish() remote.Event {
	st := t.st
	st.LastState = string(t.state)
	if t.message {
		st.LastState = "message"
	}
	switch t.state {
	case a2a.TaskStateInputRequired:
		st.TaskID, st.InputRequired = t.taskID, true
		if t.status != "" {
			if t.text.Len() > 0 {
				t.write("\n\n")
			}
			t.write(t.status)
		}
		return remote.Event{Kind: remote.EventDone, Text: t.text.String()}
	case a2a.TaskStateFailed, a2a.TaskStateRejected, a2a.TaskStateCanceled, a2a.TaskStateAuthRequired:
		msg := t.status
		if msg == "" {
			msg = "remote task ended as " + strings.ToLower(strings.TrimPrefix(string(t.state), "TASK_STATE_"))
		}
		return t.finishError("Remote agent: " + msg)
	}
	st.TaskID, st.InputRequired = "", false
	if t.text.Len() == 0 && t.status != "" {
		t.write(t.status)
	}
	return remote.Event{Kind: remote.EventDone, Text: t.text.String()}
}

func (t *turnState) finishError(msg string) remote.Event {
	t.st.TaskID, t.st.InputRequired = "", false
	t.st.LastState = string(t.state)
	return remote.Event{Kind: remote.EventError, Text: msg}
}
