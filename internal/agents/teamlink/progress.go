package teamlink

import (
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Live progress of a working task: one plain line saying what the
// teammate's turn is doing right now ("Checking Loki for the 21:00
// window…"), for the sender's Tasks tab. It is the sentence the turn
// wrote before its newest tool call, or the tool's name when it wrote
// none. Kept in memory and sent on OnTaskChange only: never written to
// the task's record, which is rewritten on every change.
//
// A session's events count for a task only while the task owns the
// session's turn (TurnOwner, the TurnGate): a turn the task still waits
// behind (a person chatting with the teammate, another task) or the turn
// after its own never shows on it.

const (
	// progressMax caps TaskView.Progress, in runes: one line of a row.
	progressMax = 140
	// progressEvery is how often one task's progress may go out; a newer
	// line inside the window waits for its end, older ones are dropped.
	progressEvery = 2 * time.Second
	// progressTextMax is how much of the text streamed since the last
	// tool call a turn keeps, in bytes: its last line is all that is read.
	progressTextMax = 2 << 10
)

// progressState is the progress of one working task. Guarded by
// Hub.progMu.
type progressState struct {
	task    a2a.TaskID
	session string
	// text is what the task's turn streamed since its last tool call.
	text string
	// shown is the line last sent, and sentAt when.
	shown  string
	sentAt time.Time
	// pending is a newer line the throttle holds; armed = its flush is
	// scheduled.
	pending string
	armed   bool
}

// progressHub is the Hub's part for live progress.
type progressHub struct {
	progMu sync.Mutex
	// progress is the progress of each task whose turn is tracked, and
	// sessions the tasks tracked in each session, in the order they
	// started (the fallback when TurnOwner is nil).
	progress map[a2a.TaskID]*progressState
	sessions map[string][]a2a.TaskID
	// TurnOwner names the task that owns sessionID's running turn ("" =
	// none, or a turn the task shares with a person's message), so a
	// session's events reach only that task. Nil = the session's oldest
	// tracked task owns it (tests). Called with progMu held: it must not
	// call back into the Hub.
	TurnOwner func(sessionID string) a2a.TaskID
	// progressEvery is the throttle window; tests shorten it.
	progressEvery time.Duration
	// progressAfter schedules a held line's flush (time.AfterFunc), and
	// progressGo runs a send off the caller's goroutine (go f()). Tests
	// replace both to run them by hand.
	progressAfter func(d time.Duration, f func())
	progressGo    func(f func())
}

// startProgress tracks task id's turn in session. Never call it with
// h.mu held.
func (h *Hub) startProgress(session string, id a2a.TaskID) {
	if session == "" || id == "" {
		return
	}
	h.progMu.Lock()
	if h.progress == nil {
		h.progress, h.sessions = map[a2a.TaskID]*progressState{}, map[string][]a2a.TaskID{}
	}
	if old := h.progress[id]; old != nil {
		h.dropProgressLocked(old)
	}
	h.progress[id] = &progressState{task: id, session: session}
	h.sessions[session] = append(h.sessions[session], id)
	h.progMu.Unlock()
}

// stopProgress forgets task id's progress: the task left working, so its
// next view carries none and a held line is never sent.
func (h *Hub) stopProgress(id a2a.TaskID) {
	h.progMu.Lock()
	if st := h.progress[id]; st != nil {
		h.dropProgressLocked(st)
	}
	h.progMu.Unlock()
}

// dropProgressLocked forgets st. Called with progMu held.
func (h *Hub) dropProgressLocked(st *progressState) {
	delete(h.progress, st.task)
	list := h.sessions[st.session]
	for i, id := range list {
		if id == st.task {
			list = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(h.sessions, st.session)
	} else {
		h.sessions[st.session] = list
	}
}

// activeProgressLocked is the progress the turn now running in session
// feeds: the task owning the turn, if tracked there; nil otherwise.
// Called with progMu held.
func (h *Hub) activeProgressLocked(session string) *progressState {
	list := h.sessions[session]
	if len(list) == 0 {
		return nil
	}
	id := list[0]
	if h.TurnOwner != nil {
		id = h.TurnOwner(session)
	}
	if st := h.progress[id]; st != nil && st.session == session {
		return st
	}
	return nil
}

// progressOf is task id's progress line, "" when none. Called with h.mu
// held (viewLocked): progMu is always taken after h.mu, never before.
func (h *Hub) progressOf(id a2a.TaskID) string {
	h.progMu.Lock()
	defer h.progMu.Unlock()
	if st := h.progress[id]; st != nil {
		return st.shown
	}
	return ""
}

// TurnText notes text a turn of sessionID streamed. Only a session whose
// turn a task owns keeps it; it is cheap for every other one.
func (h *Hub) TurnText(sessionID, delta string) {
	if delta == "" {
		return
	}
	h.progMu.Lock()
	defer h.progMu.Unlock()
	st := h.activeProgressLocked(sessionID)
	if st == nil {
		return
	}
	st.text += delta
	if over := len(st.text) - progressTextMax; over > 0 {
		// Keep the tail, cut at a rune boundary; a fence opened in the
		// part cut off stays open.
		for over < len(st.text) && !utf8.RuneStart(st.text[over]) {
			over++
		}
		cut := st.text[:over]
		st.text = st.text[over:]
		if fenceCount(cut)%2 == 1 {
			st.text = "```\n" + st.text
		}
	}
}

// TurnTool notes that a turn of sessionID called tool (its short label):
// the task owning the turn now reads the sentence written before the
// call, or "Running <tool>…". Sent at most once per progressEvery, only
// on change, off the caller's goroutine.
func (h *Hub) TurnTool(sessionID, tool string) {
	h.progMu.Lock()
	st := h.activeProgressLocked(sessionID)
	if st == nil {
		h.progMu.Unlock()
		return
	}
	line := progressLine(st.text, tool)
	st.text = ""
	send := h.offerProgressLocked(st, line)
	h.progMu.Unlock()
	if send {
		h.sendProgress(st, line)
	}
}

// offerProgressLocked decides whether line goes out now. Inside the
// throttle window it is held and a flush is scheduled for the window's
// end. Called with progMu held.
func (h *Hub) offerProgressLocked(st *progressState, line string) bool {
	if line == "" {
		return false
	}
	every := h.progressEvery
	if every <= 0 {
		every = progressEvery
	}
	now := h.now()
	if wait := every - now.Sub(st.sentAt); !st.sentAt.IsZero() && wait > 0 {
		st.pending = line
		if !st.armed {
			st.armed = true
			after := h.progressAfter
			if after == nil {
				after = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
			}
			after(wait, func() { h.flushProgress(st) })
		}
		return false
	}
	if line == st.shown {
		return false
	}
	st.shown, st.sentAt, st.pending = line, now, ""
	return true
}

// flushProgress sends the line the throttle held for st, if the task is
// still tracked and the line changed.
func (h *Hub) flushProgress(st *progressState) {
	h.progMu.Lock()
	if h.progress[st.task] != st {
		h.progMu.Unlock()
		return
	}
	st.armed = false
	line := st.pending
	st.pending = ""
	if line == "" || line == st.shown {
		h.progMu.Unlock()
		return
	}
	st.shown, st.sentAt = line, h.now()
	h.progMu.Unlock()
	h.sendProgress(st, line)
}

// sendProgress publishes st's task off the caller's goroutine (a
// provider's event reader, or a timer): publishProgress reads the live
// state when it runs, so a task that stopped meanwhile sends nothing.
// When nobody was told (the sender has not registered the task yet),
// the line is unsent, so the next call may send it.
func (h *Hub) sendProgress(st *progressState, line string) {
	run := h.progressGo
	if run == nil {
		run = func(f func()) { go f() }
	}
	run(func() {
		if !h.publishProgress(st.task) {
			h.progMu.Lock()
			if st.shown == line {
				st.shown, st.sentAt = "", time.Time{}
			}
			h.progMu.Unlock()
		}
	})
}

// publishProgress tells OnTaskChange about task id's progress, to the
// session that sent it, as any other change of the task. Nothing is
// written: progress lives in memory only. False when nobody was told.
func (h *Hub) publishProgress(id a2a.TaskID) bool {
	if h.OnTaskChange == nil {
		return false
	}
	h.saveMu.Lock()
	defer h.saveMu.Unlock()
	h.mu.Lock()
	ref := h.tasks[id]
	if ref == nil || ref.callerSession == "" || ref.started.IsZero() || ref.finished || ref.canceled {
		h.mu.Unlock()
		return false
	}
	v := h.viewLocked(id, ref, h.now())
	session, userAnswer := ref.callerSession, ref.userAnswer
	h.mu.Unlock()
	if v.Progress == "" {
		return false
	}
	busy := h.CallerBusy != nil && h.CallerBusy(session)
	v.NeedsYou = needsYou(v, userAnswer, busy)
	h.OnTaskChange(session, v)
	return true
}

// progressLine is the progress for a tool call: the last line of text
// written before it, outside code blocks, flattened to plain text and
// capped; or "Running <tool>…" when the turn wrote no such line.
func progressLine(text, tool string) string {
	var last string
	inFence := false
	for _, l := range strings.Split(text, "\n") {
		if isFence(l) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if p := plainLine(l); p != "" {
			last = p
		}
	}
	if last != "" {
		return capText(last, progressMax)
	}
	if tool = strings.TrimSpace(tool); tool != "" {
		return capText("Running "+tool+"…", progressMax)
	}
	return ""
}

// isFence reports whether l opens or closes a code block.
func isFence(l string) bool {
	l = strings.TrimSpace(l)
	return strings.HasPrefix(l, "```") || strings.HasPrefix(l, "~~~")
}

// fenceCount counts the fence lines of s.
func fenceCount(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if isFence(l) {
			n++
		}
	}
	return n
}

var (
	progressLink   = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	progressMarker = regexp.MustCompile(`^(#{1,6}\s+|>\s?|[-*+]\s+|\d+\.\s+)`)
)

// plainLine drops the markdown of one line: a link reads as its label,
// heading, quote and list markers go (only when followed by a space, so
// "-5 errors" stays), and its whitespace collapses.
func plainLine(s string) string {
	s = strings.TrimSpace(s)
	s = progressLink.ReplaceAllString(s, "$1")
	s = progressMarker.ReplaceAllString(s, "")
	s = strings.NewReplacer("**", "", "__", "", "`", "", "~~", "").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
