package teamlink

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// countReads makes readRecordFn note every file a list reads.
func countReads(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var read []string
	readRecordFn = func(path string) (taskRecord, bool) {
		mu.Lock()
		read = append(read, filepath.Base(path))
		mu.Unlock()
		return readRecord(path)
	}
	t.Cleanup(func() { readRecordFn = readRecord })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := read
		read = nil
		return out
	}
}

// forget drops every task from memory, as TaskTTL does, so a list has
// to read them back from disk.
func forget(h *Hub) {
	h.mu.Lock()
	h.tasks = map[a2a.TaskID]*taskRef{}
	h.mu.Unlock()
}

func indexOf(h *Hub) map[a2a.TaskID]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[a2a.TaskID]string{}
	for id, s := range h.fileSession {
		out[id] = s
	}
	return out
}

// SentFrom reads only the files of the session it lists: the index of
// task files per session stays right across create, reload, a file
// another process wrote, and prune.
func TestSentFromReadsOnlyItsSessionFiles(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sent := map[string][]string{}
	for _, c := range []struct{ session, to string }{
		{"sess-a", "anton"}, {"sess-a", "vera"},
		{"sess-b", "anton"}, {"sess-b", "vera"}, {"sess-b", "anton"},
	} {
		res, err := h1.Send(ctx, SendInput{CallerSession: c.session, CallerAgentID: "a-cap", To: c.to, Text: "hi"})
		if err != nil || res.State != "completed" {
			t.Fatalf("send = %+v, %v", res, err)
		}
		sent[c.session] = append(sent[c.session], res.TaskID)
	}
	idx := indexOf(h1)
	if len(idx) != 5 {
		t.Fatalf("index after create = %v", idx)
	}
	for s, ids := range sent {
		for _, id := range ids {
			if idx[a2a.TaskID(id)] != s {
				t.Fatalf("task %s indexed under %q, want %q", id, idx[a2a.TaskID(id)], s)
			}
		}
	}

	reads := countReads(t)
	// In memory and finished: no file is read at all.
	if list := h1.SentFrom("sess-a"); len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if got := reads(); len(got) != 0 {
		t.Fatalf("read with everything in memory: %v", got)
	}
	forget(h1)
	if list := h1.SentFrom("sess-a"); len(list) != 2 {
		t.Fatalf("list from disk = %+v", list)
	}
	if got := reads(); len(got) != 2 || !onlyOf(got, sent["sess-a"]) {
		t.Fatalf("sess-a read %v, want only %v", got, sent["sess-a"])
	}

	// A file this process never wrote (another wick process) is read
	// once to learn its session, then only by that session's list.
	foreign := taskRecord{TaskID: "t-foreign", AgentID: "a-anton", ToHandle: "anton", CallerAgentID: "a-cap",
		CallerSession: "sess-a", State: string(a2a.TaskStateCompleted), Finished: true,
		Started: time.Now(), Touched: time.Now()}
	writeRecord(dir, "t-foreign", foreign)
	forget(h1)
	if list := h1.SentFrom("sess-b"); len(list) != 3 {
		t.Fatalf("sess-b list = %+v", list)
	}
	if got := reads(); len(got) != 4 || !onlyOf(got, append(sent["sess-b"], "t-foreign")) {
		t.Fatalf("first sess-b list read %v", got)
	}
	forget(h1)
	if list := h1.SentFrom("sess-b"); len(list) != 3 {
		t.Fatalf("sess-b list = %+v", list)
	}
	if got := reads(); len(got) != 3 || !onlyOf(got, sent["sess-b"]) {
		t.Fatalf("second sess-b list read %v", got)
	}
	if list := h1.SentFrom("sess-a"); len(list) != 3 {
		t.Fatalf("sess-a list with the foreign task = %+v", list)
	}
	reads()

	// A new Hub over the folder builds the index while loading.
	h2, _, _ := newTestHub(func(Peer, string) string { return "" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if idx := indexOf(h2); len(idx) != 6 || idx["t-foreign"] != "sess-a" {
		t.Fatalf("index after reload = %v", idx)
	}
	reads()
	forget(h2)
	list := h2.SentFrom("sess-b")
	if len(list) != 3 {
		t.Fatalf("sess-b list after reload = %+v", list)
	}
	if got := reads(); len(got) != 3 || !onlyOf(got, sent["sess-b"]) {
		t.Fatalf("sess-b after reload read %v", got)
	}
	for i := 1; i < len(list); i++ {
		if list[i].Started.After(list[i-1].Started) {
			t.Fatalf("not newest first: %+v", list)
		}
	}

	// A file that went away leaves the index; a pruned one too.
	if err := removeTaskFile(dir, sent["sess-b"][0]); err != nil {
		t.Fatal(err)
	}
	forget(h2)
	if list := h2.SentFrom("sess-b"); len(list) != 2 {
		t.Fatalf("list after a file went = %+v", list)
	}
	if _, ok := indexOf(h2)[a2a.TaskID(sent["sess-b"][0])]; ok {
		t.Fatal("removed file still indexed")
	}
	h2.mu.Lock()
	h2.now = func() time.Time { return time.Now().Add(TaskKeep + time.Hour) }
	h2.pruned = time.Time{}
	h2.mu.Unlock()
	if list := h2.SentFrom("sess-a"); len(list) != 0 {
		t.Fatalf("list after prune = %+v", list)
	}
	if idx := indexOf(h2); len(idx) != 0 {
		t.Fatalf("index after prune = %v", idx)
	}
}

func removeTaskFile(dir, id string) error {
	path, _ := taskPath(dir, id)
	return os.Remove(path)
}

// onlyOf reports whether every file read is one of ids' files.
func onlyOf(read []string, ids []string) bool {
	want := map[string]bool{}
	for _, id := range ids {
		want[id+".json"] = true
	}
	for _, r := range read {
		if !want[r] {
			return false
		}
	}
	return true
}
