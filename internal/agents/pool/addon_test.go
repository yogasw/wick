package pool

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

func addonLayout(t *testing.T) config.Layout {
	t.Helper()
	l := config.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}
	return l
}

// The project addon is shared context; the session addon is the specific
// instruction (a sub-agent role's prompt). The specific one must come
// LAST so it refines the general one instead of being buried by it.
func TestComposeSystemAddonOrdersProjectBeforeSession(t *testing.T) {
	layout := addonLayout(t)
	p, err := project.Create(layout, project.CreateOptions{
		ID: "proj-1", Name: "proj-1",
		Defaults: project.Defaults{SystemAddon: "PROJECT RULES"},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	sess := session.Session{}
	sess.Meta.ProjectID = p.Meta.ID
	sess.Meta.SystemAddon = "ROLE PROMPT"

	got := composeSystemAddon(layout, sess)
	if got != "PROJECT RULES\n\nROLE PROMPT" {
		t.Fatalf("got %q", got)
	}
}

// Either half alone must still render, and neither should leave stray
// blank lines that would show up in the prompt.
func TestComposeSystemAddonHandlesEitherHalfMissing(t *testing.T) {
	layout := addonLayout(t)
	if _, err := project.Create(layout, project.CreateOptions{ID: "proj-2", Name: "proj-2"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	sess := session.Session{}
	sess.Meta.ProjectID = "proj-2"
	sess.Meta.SystemAddon = "ROLE ONLY"
	if got := composeSystemAddon(layout, sess); got != "ROLE ONLY" {
		t.Fatalf("session-only gave %q", got)
	}

	bare := session.Session{}
	if got := composeSystemAddon(layout, bare); got != "" {
		t.Fatalf("empty gave %q, want empty string", got)
	}
	if strings.Contains(composeSystemAddon(layout, sess), "\n\n\n") {
		t.Fatal("blank-line run leaked into the prompt")
	}
}
