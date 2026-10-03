package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

func TestUnread(t *testing.T) {
	now := time.Now()
	before, after := now.Add(-time.Minute), now.Add(time.Minute)
	cases := []struct {
		name   string
		active time.Time
		read   *time.Time
		want   bool
	}{
		{"no activity", time.Time{}, nil, false},
		{"never opened", now, nil, true},
		{"read after activity", now, &after, false},
		{"activity after read", now, &before, true},
		{"same instant", now, &now, false},
	}
	for _, c := range cases {
		if got := Unread(c.active, c.read); got != c.want {
			t.Errorf("%s: Unread = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCurrentAction(t *testing.T) {
	use := func(id, name, in string) store.TurnEvent {
		return store.TurnEvent{Type: "tool_use", ToolUseID: id, ToolName: name, ToolInput: in}
	}
	res := func(id string) store.TurnEvent { return store.TurnEvent{Type: "tool_result", ToolUseID: id} }
	cases := []struct {
		name string
		evs  []store.TurnEvent
		want string
	}{
		{"idle", nil, ""},
		{"thinking only", []store.TurnEvent{{Type: "thinking"}}, ""},
		{"tool running", []store.TurnEvent{{Type: "thinking"}, use("a", "Bash", "{}")}, "Bash"},
		{"tool finished", []store.TurnEvent{use("a", "Bash", "{}"), res("a")}, ""},
		{"second tool running", []store.TurnEvent{use("a", "Read", ""), res("a"), use("b", "Grep", "")}, "Grep"},
		{"writing after tool", []store.TurnEvent{use("a", "Read", ""), res("a"), {Type: "text"}}, ""},
		{"connector op", []store.TurnEvent{use("a", "mcp__wick__wick_execute", `{"tool_id":"conn:9f2c/query_range@acc1"}`)}, "query_range"},
	}
	for _, c := range cases {
		if got := CurrentAction(c.evs); got != c.want {
			t.Errorf("%s: CurrentAction = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestActionLabel(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"Bash", "", "Bash"},
		{"mcp__wick__wick_list", "", "wick_list"},
		{"mcp__support-tools__wick_execute", `{"tool_id":"conn:x/send_message"}`, "send_message"},
		{"mcp__wick__wick_execute", `{"calls":[]}`, "wick_execute"},
		{"mcp__wick__wick_execute", `not json`, "wick_execute"},
	}
	for _, c := range cases {
		if got := ActionLabel(c.name, c.input); got != c.want {
			t.Errorf("ActionLabel(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	long := ActionLabel("abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz", "")
	if r := []rune(long); len(r) != maxActionRunes || r[len(r)-1] != '…' {
		t.Errorf("long label not truncated: %q", long)
	}
}

func TestPreviewText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"  halo\n\n  dunia  ", "halo dunia"},
		{"## Hasil\n- **satu** dan `dua`\n> kutip", "Hasil satu dan dua kutip"},
		{"lihat [dashboard](https://x.example/a?b=c)", "lihat dashboard"},
		{"```go\nfmt.Println(1)\n```", "fmt.Println(1)"},
	}
	for _, c := range cases {
		if got := PreviewText(c.in); got != c.want {
			t.Errorf("PreviewText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := PreviewText(strings.Repeat("a", 200))
	if r := []rune(long); len(r) != maxPreviewRunes || !strings.HasSuffix(long, "…") {
		t.Errorf("long preview = %d runes %q", len(r), long)
	}
}

func TestAttentionPreviews(t *testing.T) {
	if got := AskPreview("Deploy ke prod?\nPilih satu"); got != "Needs input: Deploy ke prod? Pilih satu" {
		t.Errorf("AskPreview = %q", got)
	}
	if got := ApprovalPreview("mcp__wick__wick_execute"); got != "wick_execute — needs approval" {
		t.Errorf("ApprovalPreview = %q", got)
	}
	if got := ApprovalPreview(""); got != "Action — needs approval" {
		t.Errorf("ApprovalPreview empty = %q", got)
	}
}

func TestTailPreview(t *testing.T) {
	dir := t.TempDir()
	if got := TailPreview(filepath.Join(dir, "missing.jsonl")); got != "" {
		t.Errorf("missing file = %q", got)
	}
	p := filepath.Join(dir, "conversation.jsonl")
	var b strings.Builder
	// Old filler pushes the first turn out of the tail window.
	b.WriteString(`{"role":"user","text":"` + strings.Repeat("x", previewTailBytes) + `"}` + "\n")
	b.WriteString(`{"role":"user","text":"cek log"}` + "\n")
	b.WriteString(`{"role":"assistant","text":"**Sudah** dicek, aman."}` + "\n")
	b.WriteString(`{"role":"system","text":"turn selesai"}` + "\n")
	b.WriteString(`{"role":"assistant","text":"   "}` + "\n")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := TailPreview(p); got != "Sudah dicek, aman." {
		t.Errorf("TailPreview = %q", got)
	}
}

func TestTurnStatus(t *testing.T) {
	cases := []struct {
		meta, lifecycle, want string
	}{
		// A warm process between turns: the meta still says running.
		{"running", "idle", "idle"},
		{"running", "working", "running"},
		{"running", "spawning", "running"},
		// The process is gone; a stale running meta is not a turn.
		{"running", "", "idle"},
		{"running", "killed", "idle"},
		{"queued", "", "queued"},
		{"idle", "", "idle"},
	}
	for _, c := range cases {
		if got := TurnStatus(c.meta, c.lifecycle); got != c.want {
			t.Errorf("TurnStatus(%q, %q) = %q, want %q", c.meta, c.lifecycle, got, c.want)
		}
	}
}
