package teamlink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/rs/zerolog/log"
)

// Tasks a session sent are kept on disk, one JSON file per task under the
// directory Persist names, so get_task, list_tasks, a follow-up and the
// answer to an input_required question still work after wick restarts and
// after the in-memory record ages out (TaskTTL). A file holds ids, handles,
// the task's title and its reply — never a credential.
const (
	// TaskKeep is how long a task file is kept after its last change: a
	// week covers a question left over a weekend, and the folder stays
	// small (the reply is capped at maxStoredReply).
	TaskKeep = 7 * 24 * time.Hour
	// maxStoredReply caps the reply text written to disk.
	maxStoredReply = 64 << 10
	// orphanPoll is how often settleOrphans looks again at the tasks a
	// restart left working while a draining predecessor may finish them.
	orphanPoll = 30 * time.Second
	// interruptedReason is the reason of a task a restart left working.
	interruptedReason = "interrupted by a restart: wick restarted while the teammate was working on it. Send it again if it is still needed."
)

// taskRecord is one task as written to disk.
type taskRecord struct {
	TaskID        string `json:"task_id"`
	ContextID     string `json:"context_id,omitempty"`
	AgentID       string `json:"agent_id"`
	ToHandle      string `json:"to_handle"`
	ToName        string `json:"to_name,omitempty"`
	ToOwner       string `json:"to_owner,omitempty"`
	ToChatUser    string `json:"to_chat_user,omitempty"`
	ToRemote      bool   `json:"to_remote,omitempty"`
	CallerAgentID string `json:"caller_agent_id"`
	CallerOwner   string `json:"caller_owner,omitempty"`
	CallerSession string `json:"caller_session,omitempty"`
	Title         string `json:"title,omitempty"`
	State         string `json:"state,omitempty"`
	Finished      bool   `json:"finished,omitempty"`
	WaiterGone    bool   `json:"waiter_gone,omitempty"`
	Delivered     bool   `json:"delivered,omitempty"`
	Canceled      bool   `json:"canceled,omitempty"`
	Interrupted   bool   `json:"interrupted,omitempty"`
	// Origin and OriginUser are taskRef.origin/originUser; a record
	// written before they existed reads as the agent's task.
	Origin     string    `json:"origin,omitempty"`
	OriginUser string    `json:"origin_user,omitempty"`
	Reply      string    `json:"reply,omitempty"`
	Chat       string    `json:"chat,omitempty"`
	Answered   string    `json:"answered_in,omitempty"`
	Sent       []string  `json:"sent,omitempty"`
	Started    time.Time `json:"started_at"`
	Touched    time.Time `json:"updated_at"`
}

// Persist keeps the Hub's tasks in dir (created when missing) and loads
// the ones already there, dropping files older than TaskKeep. Call it once,
// before the Hub is used.
func (h *Hub) Persist(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dir = dir
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		rec, ok := readRecord(filepath.Join(dir, name))
		if !ok || now.Sub(rec.Touched) > TaskKeep {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		h.adoptLocked(rec, true)
	}
	for id, ref := range h.tasks {
		if h.orphanLocked(id, ref) {
			go h.watchOrphans()
			break
		}
	}
	return nil
}

// orphanLocked reports whether task id is a restart's leftover: read back
// from disk unfinished, nobody waiting on an answer to it, and not
// answered by any turn of this process. Its turn ran in a process that is
// gone, so without settleOrphans it would read "working" for TaskKeep and
// the sender, told never to resend a working task, would lose it.
func (h *Hub) orphanLocked(id a2a.TaskID, ref *taskRef) bool {
	if !ref.fromDisk || ref.finished || ref.claimed || ref.canceled {
		return false
	}
	_, running := h.inflight[ref.agentID][id]
	return !running
}

// settleOrphans settles every restart's leftover as failed (interrupted),
// persisting it and handing the reason back to a sender that stopped
// waiting, so the sender may send it again. A task a draining predecessor
// may still be running is left for later; the count of those is returned.
func (h *Hub) settleOrphans(ctx context.Context) int {
	type orphan struct {
		id   a2a.TaskID
		chat string
	}
	h.mu.Lock()
	var found []orphan
	for id, ref := range h.tasks {
		if h.orphanLocked(id, ref) {
			found = append(found, orphan{id, ref.chat})
		}
	}
	h.mu.Unlock()
	left := 0
	for _, o := range found {
		// Reads the predecessor's drain record: outside h.mu.
		if h.PredecessorBusy != nil && h.PredecessorBusy(o.chat) {
			left++
			continue
		}
		h.settle(ctx, o.id, a2a.TaskStateFailed, interruptedReason, true)
	}
	return left
}

// watchOrphans runs settleOrphans every orphanPoll until no leftover is
// waiting on a draining predecessor. The first look waits one period, so
// the sessions the reason is delivered to are up.
func (h *Hub) watchOrphans() {
	for {
		time.Sleep(h.orphanPoll)
		if h.settleOrphans(context.Background()) == 0 {
			return
		}
	}
}

// adoptLocked puts a record read from disk back in memory: the task, its
// exchange (so context_id and task_id keep working) and the session that
// answered it (so a follow-up still finds the asker). Only the load at
// start (initial) charges the task's turn to its exchange: a task read
// back later (aged out of memory, listed again) was charged when it was
// sent, and charging it on every read would run the exchange into its
// turn cap with nobody talking.
func (h *Hub) adoptLocked(rec taskRecord, initial bool) {
	id := a2a.TaskID(rec.TaskID)
	ref := refFromRecord(rec)
	if old := h.tasks[id]; old != nil && !old.touched.Before(ref.touched) {
		return
	}
	h.tasks[id] = ref
	if rec.ContextID != "" && rec.CallerOwner != "" {
		cs := h.contexts[rec.ContextID]
		if cs == nil {
			cs = &contextState{owner: rec.CallerOwner, limit: MaxContextTurns}
			h.contexts[rec.ContextID] = cs
		}
		if initial {
			cs.turns++
		}
		if ref.touched.After(cs.touched) {
			cs.touched = ref.touched
		}
	}
	if rec.Answered != "" {
		if cur, ok := h.answered[rec.Answered]; !ok || h.tasks[cur] == nil || h.tasks[cur].touched.Before(ref.touched) {
			h.answered[rec.Answered] = id
		}
	}
}

func refFromRecord(rec taskRecord) *taskRef {
	ref := &taskRef{
		agentID: rec.AgentID, callerAgentID: rec.CallerAgentID, callerOwner: rec.CallerOwner, callerSession: rec.CallerSession,
		to:         Peer{ID: rec.AgentID, OwnerID: rec.ToOwner, Handle: rec.ToHandle, Name: rec.ToName, ChatUser: rec.ToChatUser, Remote: rec.ToRemote},
		waiterGone: rec.WaiterGone, finished: rec.Finished, delivered: rec.Delivered, canceled: rec.Canceled,
		reply: rec.Reply, state: a2a.TaskState(rec.State), interrupted: rec.Interrupted, fromDisk: true,
		touched: rec.Touched, contextID: rec.ContextID, title: rec.Title, started: rec.Started,
		chat: rec.Chat, answered: rec.Answered,
	}
	if rec.Origin == OriginUser {
		ref.origin, ref.originUser = OriginUser, rec.OriginUser
	}
	for _, s := range rec.Sent {
		ref.markSent(s)
	}
	return ref
}

func (r *taskRef) record(id a2a.TaskID) taskRecord {
	rec := taskRecord{
		TaskID: string(id), ContextID: r.contextID, AgentID: r.agentID,
		ToHandle: r.to.Handle, ToName: r.to.Name, ToOwner: r.to.OwnerID, ToChatUser: r.to.ChatUser, ToRemote: r.to.Remote,
		CallerAgentID: r.callerAgentID, CallerOwner: r.callerOwner, CallerSession: r.callerSession,
		Title: r.title, State: string(r.state),
		Finished: r.finished, WaiterGone: r.waiterGone, Delivered: r.delivered, Canceled: r.canceled, Interrupted: r.interrupted,
		Reply: r.reply, Chat: r.chat, Answered: r.answered,
		Started: r.started, Touched: r.touched,
		Origin: r.origin, OriginUser: r.originUser,
	}
	if len(rec.Reply) > maxStoredReply {
		rec.Reply = rec.Reply[:maxStoredReply] + "…"
	}
	for s := range r.sent {
		rec.Sent = append(rec.Sent, s)
	}
	return rec
}

// persist writes task id to disk and tells OnTaskChange. The snapshot is
// taken under saveMu, so two saves (and two events) of one task land in
// the order they were taken. Never call it with h.mu held.
func (h *Hub) persist(id a2a.TaskID) {
	h.saveMu.Lock()
	defer h.saveMu.Unlock()
	h.mu.Lock()
	ref, dir := h.tasks[id], h.dir
	if ref == nil {
		h.mu.Unlock()
		return
	}
	var view *TaskView
	var userAnswer bool
	session := ref.callerSession
	if h.OnTaskChange != nil && session != "" && !ref.started.IsZero() {
		v := h.viewLocked(id, ref, h.now())
		view, userAnswer = &v, ref.userAnswer
	}
	write := dir != "" && ref.callerAgentID != ""
	var rec taskRecord
	if write {
		rec = ref.record(id)
	}
	h.mu.Unlock()
	if write {
		writeRecord(dir, id, rec)
	}
	if view != nil {
		// CallerBusy reads the pool, so it runs outside h.mu.
		busy := h.CallerBusy != nil && h.CallerBusy(session)
		view.NeedsYou = needsYou(*view, userAnswer, busy)
		h.OnTaskChange(session, *view)
	}
}

// RefreshNeedsYou sends OnTaskChange again for the questions sessionID's
// tasks wait on (input_required, nobody answering): whether they need the
// user depends on the session being idle, which changes without the task
// changing. Call it when a turn of sessionID ended.
func (h *Hub) RefreshNeedsYou(sessionID string) {
	if h.OnTaskChange == nil || sessionID == "" {
		return
	}
	h.saveMu.Lock()
	defer h.saveMu.Unlock()
	h.mu.Lock()
	now := h.now()
	var views []TaskView
	for id, ref := range h.tasks {
		if ref.callerSession == sessionID && !ref.started.IsZero() && !ref.claimed && !ref.userAnswer &&
			ref.finished && ref.state == a2a.TaskStateInputRequired {
			views = append(views, h.viewLocked(id, ref, now))
		}
	}
	h.mu.Unlock()
	if len(views) == 0 {
		return
	}
	busy := h.CallerBusy != nil && h.CallerBusy(sessionID)
	for _, v := range views {
		v.NeedsYou = needsYou(v, false, busy)
		h.OnTaskChange(sessionID, v)
	}
}

// writeRecord writes rec as task id's file in dir, atomically.
func writeRecord(dir string, id a2a.TaskID, rec taskRecord) {
	path, ok := taskPath(dir, string(id))
	if !ok {
		return
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		// The task still works from memory; it is gone from list_tasks
		// after a restart.
		log.Warn().Str("task_id", string(id)).Err(err).Msg("teamlink: task not saved")
	}
}

// loadLocked reads task id from disk when memory no longer has it (aged
// out, or a task another wick process finished), adopting it.
func (h *Hub) loadLocked(id a2a.TaskID) *taskRef {
	if h.dir == "" {
		return h.tasks[id]
	}
	path, ok := taskPath(h.dir, string(id))
	if !ok {
		return h.tasks[id]
	}
	if rec, ok := readRecord(path); ok && h.now().Sub(rec.Touched) <= TaskKeep {
		h.adoptLocked(rec, false)
	}
	return h.tasks[id]
}

// records lists the records in dir sent from session, skipping the tasks
// in skip (memory already holds them as finished: their file adds
// nothing). It reads files, so it runs without h.mu.
func records(dir, session string, skip map[a2a.TaskID]bool) []taskRecord {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []taskRecord
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if e.IsDir() || !ok || skip[a2a.TaskID(id)] {
			continue
		}
		if rec, ok := readRecord(filepath.Join(dir, e.Name())); ok && rec.CallerSession == session {
			out = append(out, rec)
		}
	}
	return out
}

// pruneFilesLocked drops task files past TaskKeep.
func (h *Hub) pruneFilesLocked(now time.Time) {
	if h.dir == "" {
		return
	}
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && !e.IsDir() && now.Sub(info.ModTime()) > TaskKeep {
			_ = os.Remove(filepath.Join(h.dir, e.Name()))
		}
	}
}

func readRecord(path string) (taskRecord, bool) {
	var rec taskRecord
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &rec) != nil || rec.TaskID == "" {
		return rec, false
	}
	return rec, true
}

// taskPath is the file of task id; false for an id that is not a plain
// file name.
func taskPath(dir, id string) (string, bool) {
	if id == "" || id != filepath.Base(id) || strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		return "", false
	}
	return filepath.Join(dir, id+".json"), true
}

// sameText reduces a reply to what dedupe compares: no markup, no case,
// single spaces. A remote that re-posts its answer with other formatting
// still matches.
func sameText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '*', '_', '`', '#', '>', '~':
			return -1
		}
		return r
	}, strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

// textKey is the dedupe key of a delivered text.
func textKey(s string) string {
	sum := sha256.Sum256([]byte(sameText(s)))
	return hex.EncodeToString(sum[:12])
}

// markSent notes a text key the caller already received.
func (r *taskRef) markSent(key string) {
	if r.sent == nil {
		r.sent = map[string]bool{}
	}
	r.sent[key] = true
}

// alreadySent reports whether text adds nothing to what the caller got
// for this task: the same text again, or a part of the reply it has.
func (r *taskRef) alreadySent(text string) bool {
	if r.sent[textKey(text)] {
		return true
	}
	n := sameText(text)
	return n != "" && r.reply != "" && strings.Contains(sameText(r.reply), n)
}
