package provider

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// A same-type switch asks the target type's registered HistoryCarrier
// (omp and opencode register theirs from their own packages): "continuing"
// only when it says the history is reachable, else "starting fresh" with
// its reason and the id dropped. A type with no carrier always carries.
func TestSwitchNoticeFollowsHistoryCarrier(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeOMP, Name: "waba"})
	saveSeed(t, Instance{Type: TypeOMP, Name: "yoga"})
	saveSeed(t, Instance{Type: TypeOpencode, Name: "a"})
	saveSeed(t, Instance{Type: TypeOpencode, Name: "b"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "claude"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "work"})
	layout := config.NewLayout(t.TempDir())

	var asked []string
	RegisterHistoryCarrier(TypeOMP, func(from, to Instance, id string) HistoryCarry {
		asked = append(asked, from.Name+"→"+to.Name+":"+id)
		if id == "omp-gone" {
			return HistoryCarry{Reason: "omp transcript omp-gone was not found in any omp profile"}
		}
		return HistoryCarry{}
	})
	RegisterHistoryCarrier(TypeOpencode, func(from, to Instance, id string) HistoryCarry {
		if from.Name == "b" {
			return HistoryCarry{Reason: "the opencode data of b is gone"}
		}
		return HistoryCarry{Copied: true}
	})
	t.Cleanup(func() { RegisterHistoryCarrier(TypeOMP, nil); RegisterHistoryCarrier(TypeOpencode, nil) })

	run := func(t *testing.T, sid, from, to, id string) (string, string) {
		t.Helper()
		seedSession(t, layout, sid, "main", from)
		if err := session.SetCLISessionID(layout, sid, "main", id); err != nil {
			t.Fatal(err)
		}
		if err := Switch(layout, nopPool{}, sid, "main", to, SwitchOptions{}); err != nil {
			t.Fatalf("switch: %v", err)
		}
		turns := readConv(t, layout.SessionConversation(sid))
		loaded, _ := session.Load(layout, sid)
		return turns[len(turns)-1].Extras["note"], loaded.Agents[0].CLISessionID
	}

	cases := []struct {
		name, sid, from, to, id string
		note, wantID            string
	}{
		{"omp: reachable", "s-omp-ok", "omp/waba", "omp/yoga", "omp-1", "continuing the same conversation — omp/yoga resumes", "omp-1"},
		{"omp: unreachable", "s-omp-miss", "omp/waba", "omp/yoga", "omp-gone", "starting fresh on omp/yoga — omp transcript omp-gone", ""},
		{"opencode: copied", "s-oc-ok", "opencode/a", "opencode/b", "ses_1", "copied into opencode/b", "ses_1"},
		{"opencode: source gone", "s-oc-miss", "opencode/b", "opencode/a", "ses_2", "starting fresh on opencode/a", ""},
		{"claude: no carrier, shared store", "s-cl", "claude/claude", "claude/work", "cl-1", "continuing the same conversation — claude/work resumes", "cl-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			note, id := run(t, c.sid, c.from, c.to, c.id)
			if !strings.Contains(note, c.note) || id != c.wantID {
				t.Fatalf("note %q id %q", note, id)
			}
		})
	}
	if len(asked) != 2 || asked[0] != "waba→yoga:omp-1" {
		t.Fatalf("omp carrier calls = %v", asked)
	}
}

func TestSwitchNoteTexts(t *testing.T) {
	cases := []struct {
		carry      HistoryCarry
		same, hist bool
		want       string
	}{
		{HistoryCarry{}, true, true, "continuing the same conversation — claude/x resumes"},
		{HistoryCarry{Copied: true}, true, true, "copied into claude/x before its next turn"},
		{HistoryCarry{Reason: "gone"}, true, false, "starting fresh on claude/x — gone."},
		{HistoryCarry{}, false, true, "resuming claude/x's own earlier turns"},
		{HistoryCarry{}, false, false, "won't see earlier turns from other providers"},
	}
	for _, c := range cases {
		if got := switchNote("claude/x", c.carry, c.same, c.hist); !strings.Contains(got, c.want) {
			t.Errorf("%+v: %q", c, got)
		}
	}
}
