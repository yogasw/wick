package manager

import "testing"

func TestPickTabWS(t *testing.T) {
	tabs := []endpointsTab{
		{Index: 0, WSDebuggerURL: "ws://127.0.0.1:1/devtools/page/a"},
		{Index: 1, WSDebuggerURL: "ws://127.0.0.1:1/devtools/page/b"},
		{Index: 2, WSDebuggerURL: "ws://127.0.0.1:1/devtools/page/c"},
	}

	tests := []struct {
		name  string
		tabs  []endpointsTab
		index int
		want  string
	}{
		{"first tab", tabs, 0, "ws://127.0.0.1:1/devtools/page/a"},
		{"middle tab", tabs, 1, "ws://127.0.0.1:1/devtools/page/b"},
		{"last tab", tabs, 2, "ws://127.0.0.1:1/devtools/page/c"},
		{"out of range high", tabs, 3, ""},
		{"negative", tabs, -1, ""},
		{"empty", nil, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickTabWS(tc.tabs, tc.index); got != tc.want {
				t.Errorf("pickTabWS(%d) = %q, want %q", tc.index, got, tc.want)
			}
		})
	}
}

// Matching is by Index, not slice position — a gap in the index sequence (a tab
// closed mid-list) must not shift which URL a given index resolves to.
func TestPickTabWS_MatchesByIndexNotPosition(t *testing.T) {
	tabs := []endpointsTab{
		{Index: 0, WSDebuggerURL: "a"},
		{Index: 2, WSDebuggerURL: "c"}, // index 1 was closed
	}
	if got := pickTabWS(tabs, 2); got != "c" {
		t.Errorf("pickTabWS(2) = %q, want %q", got, "c")
	}
	if got := pickTabWS(tabs, 1); got != "" {
		t.Errorf("pickTabWS(1) = %q, want empty (index 1 gone)", got)
	}
}
