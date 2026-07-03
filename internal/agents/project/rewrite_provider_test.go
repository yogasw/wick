package project

import "testing"

func TestRewriteProvider(t *testing.T) {
	layout := newLayout(t)

	mustCreate := func(id, prov string) {
		p, err := Create(layout, CreateOptions{ID: id, Name: id, Defaults: Defaults{Provider: prov}})
		if err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		_ = p
	}
	mustCreate("a", "claude/abc")
	mustCreate("b", "claude/abc")
	mustCreate("c", "claude/claude") // unrelated — must stay put

	n, err := RewriteProvider(layout, "claude/abc", "claude/abc_b")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if n != 2 {
		t.Fatalf("migrated count = %d, want 2", n)
	}

	for _, id := range []string{"a", "b"} {
		p, err := Load(layout, id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if p.Meta.Defaults.Provider != "claude/abc_b" {
			t.Fatalf("project %s provider = %q, want claude/abc_b", id, p.Meta.Defaults.Provider)
		}
	}
	c, _ := Load(layout, "c")
	if c.Meta.Defaults.Provider != "claude/claude" {
		t.Fatalf("unrelated project c rewritten to %q", c.Meta.Defaults.Provider)
	}
}

func TestRewriteProviderNoop(t *testing.T) {
	layout := newLayout(t)
	if n, err := RewriteProvider(layout, "", "claude/x"); err != nil || n != 0 {
		t.Fatalf("empty oldKey: n=%d err=%v, want 0/nil", n, err)
	}
	if n, err := RewriteProvider(layout, "claude/x", "claude/x"); err != nil || n != 0 {
		t.Fatalf("same key: n=%d err=%v, want 0/nil", n, err)
	}
}
