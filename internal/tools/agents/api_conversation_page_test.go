package agents

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The conversation endpoint's reply-jump modes, end to end: `around`
// answers the window either side of the target, `after` pages forward 20
// at a time, and an unknown id is a 404 "turn not found" — not the latest
// window.
func TestPageTurnsHandler(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	chat := openChat(t, owner, p.ID, false)

	var lines []string
	for i := 1; i <= 30; i++ {
		b, _ := json.Marshal(map[string]any{"turn_id": fmt.Sprintf("t%d", i), "role": "user", "text": fmt.Sprintf("m%d", i)})
		lines = append(lines, string(b))
	}
	path := globalLayout.SessionConversation(chat)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	type page struct {
		Turns []struct {
			TurnID string `json:"turn_id"`
		} `json:"turns"`
		HasMore      bool   `json:"has_more"`
		HasMoreAfter bool   `json:"has_more_after"`
		Error        string `json:"error"`
	}
	get := func(query string) (int, page) {
		w, c := teamReq(t, owner, http.MethodGet, "/?"+query, nil, map[string]string{"id": chat})
		apiSessionConversation(c)
		var pg page
		_ = json.Unmarshal(w.Body.Bytes(), &pg)
		return w.Code, pg
	}
	span := func(pg page) string {
		if len(pg.Turns) == 0 {
			return "-"
		}
		return fmt.Sprintf("%s..%s/%d", pg.Turns[0].TurnID, pg.Turns[len(pg.Turns)-1].TurnID, len(pg.Turns))
	}

	cases := []struct {
		query          string
		span           string
		more, moreNext bool
	}{
		{"around=t15", "t5..t25/21", true, true},
		{"around=t3", "t1..t13/13", false, true},
		{"after=t1", "t2..t21/20", false, true},   // no limit → afterPageLimit
		{"after=t21", "t22..t30/9", false, false}, // reaches the latest turn
		{"after=t30", "-", false, false},          // already at the latest
		{"limit=5&before=t10", "t5..t9/5", true, false},
	}
	for _, tc := range cases {
		code, pg := get(tc.query)
		if code != http.StatusOK || span(pg) != tc.span || pg.HasMore != tc.more || pg.HasMoreAfter != tc.moreNext {
			t.Errorf("%s: %d %s more=%v next=%v, want 200 %s more=%v next=%v",
				tc.query, code, span(pg), pg.HasMore, pg.HasMoreAfter, tc.span, tc.more, tc.moreNext)
		}
	}
	for _, q := range []string{"around=gone", "after=gone"} {
		code, pg := get(q)
		if code != http.StatusNotFound || pg.Error != "turn not found" || len(pg.Turns) != 0 {
			t.Errorf("%s: %d %+v, want 404 turn not found", q, code, pg)
		}
	}
}
