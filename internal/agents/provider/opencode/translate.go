package opencode

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// translate.go turns the server's SSE bus (GET /event) into the lines
// `opencode run --format json` prints, so event.OpencodeParser reads a
// server turn exactly like a run turn. The mapping mirrors run.ts's own
// event loop:
//
//	message.part.updated tool (completed|error) → {"type":"tool_use","part":…}
//	message.part.updated step-start             → {"type":"step_start","part":…}
//	message.part.updated step-finish            → {"type":"step_finish","part":…}
//	message.part.updated text      (time.end)   → {"type":"text","part":…}
//	message.part.updated reasoning (time.end)   → {"type":"reasoning","part":…}
//	session.error                               → {"type":"error","error":…}
//	session.status idle / session.idle          → end of the turn
//
// The bus carries every session of the directory, so everything is
// filtered on sessionID.

// sseEvent is one `data:` frame of GET /event.
type sseEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

// readSSE calls fn for every data frame on r until fn returns false or r
// ends. Frames can be large (a part carries the whole tool output), so
// lines are not length-capped.
func readSSE(r io.Reader, fn func(sseEvent) bool) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var data strings.Builder
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "" && data.Len() > 0:
			var ev sseEvent
			if json.Unmarshal([]byte(data.String()), &ev) == nil && ev.Type != "" {
				if !fn(ev) {
					return nil
				}
			}
			data.Reset()
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// translator converts one session's bus events into run-format lines.
type translator struct {
	sessionID string
	now       func() time.Time
	userMsgs  map[string]bool
	started   bool // the session went busy (or failed) since the prompt
	// replied: the turn produced something — a step, text, tool call or
	// error. An idle with nothing before it is a run opencode dropped
	// without a word (seen with a cwd that does not exist).
	replied bool
	// running: tool calls already announced as started (callID), so each
	// is announced once however many updates the server streams for it.
	running map[string]bool
	// emitted: parts already passed on (part id). A resync after a dropped
	// stream replays the session's stored parts, and each must reach the
	// agent once whichever path brought it.
	emitted map[string]bool
	// stopped: the last step seen finished with reason "stop" — the model's
	// answer is complete even if the idle that ends the turn was missed.
	stopped bool
}

// userCount is how many of this session's user messages the stream has
// shown — i.e. how many prompts the server has actually taken in.
func (t *translator) userCount() int { return len(t.userMsgs) }

func newTranslator(sessionID string) *translator {
	return &translator{sessionID: sessionID, now: time.Now, userMsgs: map[string]bool{}, running: map[string]bool{}, emitted: map[string]bool{}}
}

type busPart struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
	Synthetic bool   `json:"synthetic"`
	Reason    string `json:"reason"`
	Time      *struct {
		End int64 `json:"end"`
	} `json:"time"`
	State *struct {
		Status string `json:"status"`
	} `json:"state"`
}

// feed returns the run-format lines for ev and whether the turn is over.
func (t *translator) feed(ev sseEvent) (lines [][]byte, done bool) {
	switch ev.Type {
	case "message.updated":
		var p struct {
			Info struct {
				ID        string `json:"id"`
				Role      string `json:"role"`
				SessionID string `json:"sessionID"`
			} `json:"info"`
		}
		if json.Unmarshal(ev.Properties, &p) == nil && p.Info.SessionID == t.sessionID && p.Info.Role == "user" {
			t.userMsgs[p.Info.ID] = true
		}
	case "message.part.updated":
		var p struct {
			Part json.RawMessage `json:"part"`
		}
		if json.Unmarshal(ev.Properties, &p) != nil {
			return nil, false
		}
		var part busPart
		if json.Unmarshal(p.Part, &part) != nil || part.SessionID != t.sessionID || t.userMsgs[part.MessageID] {
			return nil, false
		}
		kind := ""
		switch part.Type {
		case "tool":
			if part.State != nil && (part.State.Status == "completed" || part.State.Status == "error") {
				kind = "tool_use"
			} else if part.State != nil && part.State.Status == "running" && part.CallID != "" && !t.running[part.CallID] {
				// A tool is only reported once it has finished, so a long
				// one (a sub-agent, a build) leaves the stream silent and
				// looks like a stalled turn. Announce the start, once, so
				// the agent knows a tool is in flight.
				t.running[part.CallID] = true
				kind = "tool_running"
			}
		case "step-start":
			kind = "step_start"
			t.stopped = false
		case "step-finish":
			kind = "step_finish"
			t.stopped = part.Reason == "stop"
		case "text":
			if part.Time != nil && part.Time.End != 0 && !part.Synthetic {
				kind = "text"
			}
		case "reasoning":
			if part.Time != nil && part.Time.End != 0 {
				kind = "reasoning"
			}
		}
		if kind != "" && kind != "tool_running" && part.ID != "" {
			if t.emitted[part.ID] {
				return nil, false
			}
			t.emitted[part.ID] = true
		}
		if kind != "" {
			t.started = true
			t.replied = true
			lines = append(lines, t.line(kind, "part", p.Part))
		}
	case "session.error":
		var p struct {
			SessionID string          `json:"sessionID"`
			Error     json.RawMessage `json:"error"`
		}
		if json.Unmarshal(ev.Properties, &p) == nil && p.SessionID == t.sessionID {
			t.started = true
			t.replied = true
			lines = append(lines, t.line("error", "error", p.Error))
		}
	case "session.status":
		var p struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if json.Unmarshal(ev.Properties, &p) == nil && p.SessionID == t.sessionID {
			switch p.Status.Type {
			case "busy", "retry":
				t.started = true
			case "idle":
				return nil, t.started
			}
		}
	case "session.idle":
		var p struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.Properties, &p) == nil && p.SessionID == t.sessionID {
			return nil, t.started
		}
	}
	return lines, false
}

func (t *translator) line(kind, field string, v json.RawMessage) []byte {
	if len(v) == 0 {
		v = json.RawMessage("null")
	}
	b, _ := json.Marshal(map[string]any{
		"type":      kind,
		"timestamp": t.now().UnixMilli(),
		"sessionID": t.sessionID,
		field:       v,
	})
	return append(b, '\n')
}

// noticeLine is a run-format text frame for a note wick itself adds to
// the reply (a finished, non-synthetic text part, as opencode emits).
func noticeLine(sessionID, text string) []byte {
	now := time.Now().UnixMilli()
	b, _ := json.Marshal(map[string]any{
		"type":      "text",
		"timestamp": now,
		"sessionID": sessionID,
		"part": map[string]any{
			"id": "prt_wick_notice", "sessionID": sessionID, "type": "text", "text": text,
			"time": map[string]any{"start": now, "end": now},
		},
	})
	return append(b, '\n')
}

// errorLine is a run-format error frame for a failure wick itself hit
// (server down, session gone) so the chat shows why the turn ended.
func errorLine(sessionID, msg string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":      "error",
		"timestamp": time.Now().UnixMilli(),
		"sessionID": sessionID,
		"error":     map[string]any{"name": "WickError", "data": map[string]any{"message": msg}},
	})
	return append(b, '\n')
}
