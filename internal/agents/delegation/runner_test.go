package delegation

import (
	"path/filepath"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

func testLayout(t *testing.T) agentconfig.Layout {
	t.Helper()
	return agentconfig.NewLayout(filepath.Join(t.TempDir(), "wick"))
}

func newChildSession(t *testing.T, layout agentconfig.Layout, id string) {
	t.Helper()
	if _, err := session.Create(t.Context(), layout, session.CreateOptions{
		ID: id, Origin: session.OriginUI, ParentSessionID: "parent-1",
	}); err != nil {
		t.Fatalf("create child session: %v", err)
	}
}

// A sub-agent's session is hidden from the conversation list, so a title
// it derives itself is a tool call nobody will ever read. Pre-titling is
// what stops the standing "set a title" rule from firing.
func TestTitleChildFromTaskMarksTheTitleCustom(t *testing.T) {
	layout := testLayout(t)
	newChildSession(t, layout, "sub-title-1")

	if err := titleChildFromTask(layout, "sub-title-1", "Review auth.go for nil derefs"); err != nil {
		t.Fatalf("title child: %v", err)
	}
	sess, err := session.Load(layout, "sub-title-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !sess.Meta.TitleCustom {
		t.Fatal("title_custom must be true, or the agent still titles itself")
	}
	if sess.Meta.Label != "Review auth.go for nil derefs" {
		t.Fatalf("label = %q", sess.Meta.Label)
	}
}

func TestTitleChildFromTaskUsesTheFirstLineAndTruncates(t *testing.T) {
	layout := testLayout(t)
	newChildSession(t, layout, "sub-title-2")

	long := strings.Repeat("a", 200) + "\nsecond line"
	if err := titleChildFromTask(layout, "sub-title-2", long); err != nil {
		t.Fatalf("title child: %v", err)
	}
	sess, _ := session.Load(layout, "sub-title-2")
	if strings.Contains(sess.Meta.Label, "second line") {
		t.Fatalf("used more than the first line: %q", sess.Meta.Label)
	}
	if r := []rune(sess.Meta.Label); len(r) > childTitleRunes+1 {
		t.Fatalf("not truncated: %d runes", len(r))
	}
}

// A human's title outranks the task. Overwriting it would silently undo
// a deliberate choice on every re-delegation into the same session.
func TestTitleChildFromTaskNeverOverwritesAnExistingTitle(t *testing.T) {
	layout := testLayout(t)
	newChildSession(t, layout, "sub-title-3")

	sess, _ := session.Load(layout, "sub-title-3")
	sess.Meta.Label = "Chosen by a human"
	sess.Meta.TitleCustom = true
	if err := session.SaveMeta(layout, "sub-title-3", sess.Meta); err != nil {
		t.Fatalf("seed title: %v", err)
	}

	if err := titleChildFromTask(layout, "sub-title-3", "some other task"); err != nil {
		t.Fatalf("title child: %v", err)
	}
	got, _ := session.Load(layout, "sub-title-3")
	if got.Meta.Label != "Chosen by a human" {
		t.Fatalf("overwrote a human's title: %q", got.Meta.Label)
	}
}

func TestTitleChildFromTaskFallsBackOnAnEmptyTask(t *testing.T) {
	layout := testLayout(t)
	newChildSession(t, layout, "sub-title-4")

	if err := titleChildFromTask(layout, "sub-title-4", "   \n  "); err != nil {
		t.Fatalf("title child: %v", err)
	}
	sess, _ := session.Load(layout, "sub-title-4")
	if sess.Meta.Label == "" || !sess.Meta.TitleCustom {
		t.Fatalf("empty task left the session untitled: %+v", sess.Meta)
	}
}
