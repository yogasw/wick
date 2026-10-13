package agents

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// P60 S4: "Hide in-between messages in channels" reads on for an agent
// that never set it, and saving it off flips the item and the channels'
// cached read at once.
func TestHideInterimSettingRoundTrip(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	p, sid := teamChat(t, owner)
	read := func() bool {
		t.Helper()
		w, c := teamReq(t, owner, http.MethodGet, "/", nil, map[string]string{"id": p.ID})
		apiTeamAgentGet(c)
		var a struct {
			Hide *bool `json:"channel_hide_interim"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &a)
		if a.Hide == nil {
			t.Fatal("channel_hide_interim missing from the item")
		}
		return *a.Hide
	}
	if !read() || !TeamHidesInterimCached(sid) {
		t.Fatal("default must read on")
	}
	w, c := teamReq(t, owner, http.MethodPatch, "/", map[string]any{"channel_hide_interim": false}, map[string]string{"id": p.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	if read() {
		t.Fatal("item still reads on after the change")
	}
	if TeamHidesInterimCached(sid) {
		t.Fatal("cached read still hides after the change")
	}
}

// A write drops the reads past the TTL, so the cache holds only recent
// sessions.
func TestStoreSwitchSeenPrunes(t *testing.T) {
	var m sync.Map
	m.Store("old", showTeammatesSeen{show: true, at: time.Now().Add(-2 * showTeammatesTTL)})
	m.Store("fresh", showTeammatesSeen{show: true, at: time.Now()})
	storeSwitchSeen(&m, "new", false)
	if _, ok := m.Load("old"); ok {
		t.Fatal("an entry past the TTL survived the write")
	}
	if _, ok := m.Load("fresh"); !ok {
		t.Fatal("a fresh entry was dropped")
	}
	if v, ok := m.Load("new"); !ok || v.(showTeammatesSeen).show {
		t.Fatal("the new read was not stored")
	}
}
