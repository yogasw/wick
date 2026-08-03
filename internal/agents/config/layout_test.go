package config

import (
	"path/filepath"
	"testing"
)

// A plain session keeps the flat path it has always had, and a sub-agent
// ID resolves one `subagents/<seg>` level per ancestor. The legacy case
// is the reason this change ships without a migration: IDs minted before
// nesting carry no separator, so they still land on their old folder.
func TestSessionDir(t *testing.T) {
	l := NewLayout(filepath.Join("base"))
	sessions := l.SessionsDir()

	cases := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "plain session is flat",
			id:   "a1b2c3d4-root",
			want: filepath.Join(sessions, "a1b2c3d4-root"),
		},
		{
			name: "child nests under its parent",
			id:   SubSessionID("a1b2c3d4-root", "9f2c81ab40de"),
			want: filepath.Join(sessions, "a1b2c3d4-root", "subagents", "9f2c81ab40de"),
		},
		{
			name: "grandchild nests one level deeper",
			id:   SubSessionID(SubSessionID("a1b2c3d4-root", "9f2c81ab40de"), "71ee03cc95a1"),
			want: filepath.Join(sessions, "a1b2c3d4-root", "subagents", "9f2c81ab40de",
				"subagents", "71ee03cc95a1"),
		},
		{
			name: "legacy sub-<uuid> stays flat",
			id:   "sub-3f1c9a70-2b44-4d6e-9d0e-5a1c8e2f77b1",
			want: filepath.Join(sessions, "sub-3f1c9a70-2b44-4d6e-9d0e-5a1c8e2f77b1"),
		},
		{
			name: "single dashes are not a separator",
			id:   "slack-T0123-1699999999-000100",
			want: filepath.Join(sessions, "slack-T0123-1699999999-000100"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := l.SessionDir(tc.id); got != tc.want {
				t.Fatalf("SessionDir(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestParentOfSubSession(t *testing.T) {
	root := "a1b2c3d4-root"
	child := SubSessionID(root, "9f2c81ab40de")
	grandchild := SubSessionID(child, "71ee03cc95a1")

	if got := ParentOfSubSession(root); got != "" {
		t.Fatalf("a plain session has no parent, got %q", got)
	}
	if got := ParentOfSubSession(child); got != root {
		t.Fatalf("parent of child = %q, want %q", got, root)
	}
	// The LAST separator wins, so a grandchild resolves to its immediate
	// parent rather than to the root of the tree.
	if got := ParentOfSubSession(grandchild); got != child {
		t.Fatalf("parent of grandchild = %q, want %q", got, child)
	}
}

// Session-scoped files must follow the session into its nested folder —
// a child whose transcript still landed at the top level would survive
// the deletion of its parent.
func TestSessionFilesFollowTheNesting(t *testing.T) {
	l := NewLayout("base")
	child := SubSessionID("root", "9f2c81ab40de")
	dir := l.SessionDir(child)

	for _, tc := range []struct {
		name string
		got  string
	}{
		{"meta.json", l.SessionMeta(child)},
		{"conversation.jsonl", l.SessionConversation(child)},
		{"thinking", l.SessionThinkingDir(child)},
		{"workspace.json", l.SessionWorkspace(child)},
	} {
		if want := filepath.Join(dir, tc.name); tc.got != want {
			t.Fatalf("%s = %q, want %q", tc.name, tc.got, want)
		}
	}
}
