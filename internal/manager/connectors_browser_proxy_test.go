package manager

import (
	"encoding/json"
	"testing"
)

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

func TestCDPGate(t *testing.T) {
	cases := []struct {
		mode, msg string
		pass      bool
	}{
		{"view", `{"id":1,"method":"Page.startScreencast","params":{}}`, true},
		{"view", `{"id":2,"method":"Page.screencastFrameAck","params":{"sessionId":1}}`, true},
		{"view", `{"id":3,"method":"Runtime.evaluate","params":{"expression":"1"}}`, true},
		{"view", `{"id":4,"method":"Runtime.evaluate","params":{"expression":"document.title"}}`, false},
		{"view", `{"id":5,"method":"Input.dispatchMouseEvent","params":{}}`, false},
		{"view", `{"id":6,"method":"Page.navigate","params":{"url":"https://example.com"}}`, false},
		{"view", `{"id":7,"method":"Emulation.setDeviceMetricsOverride","params":{}}`, false},
		{"full", `{"id":8,"method":"Input.dispatchKeyEvent","params":{}}`, true},
		{"full", `{"id":9,"method":"Page.navigate","params":{"url":"https://example.com"}}`, true},
		{"full", `{"id":10,"method":"Runtime.evaluate","params":{"expression":"navigator.clipboard"}}`, true},
		{"full", `{"id":11,"method":"Emulation.setDeviceMetricsOverride","params":{}}`, false},
		{"full", `{"id":12,"method":"Emulation.setEmulatedMedia","params":{}}`, false},
		{"full", `{"id":13,"method":"DOM.setAttributeValue","params":{}}`, false},
		{"view", `not json`, false},
	}
	for _, tc := range cases {
		reply := cdpGate(tc.mode, []byte(tc.msg))
		if tc.pass && reply != nil {
			t.Errorf("%s %s: refused (%s), want forwarded", tc.mode, tc.msg, reply)
		}
		if !tc.pass && reply == nil {
			t.Errorf("%s %s: forwarded, want refused", tc.mode, tc.msg)
		}
	}
}

func TestCDPGate_RefusalRepliesWithSameID(t *testing.T) {
	reply := cdpGate("view", []byte(`{"id":42,"method":"Emulation.setDeviceMetricsOverride","params":{}}`))
	var got struct {
		ID    int64 `json:"id"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(reply, &got); err != nil {
		t.Fatalf("reply %q: %v", reply, err)
	}
	if got.ID != 42 || got.Error.Message == "" {
		t.Fatalf("reply = %+v", got)
	}
}

func TestPanelModeDefaultsToView(t *testing.T) {
	for _, q := range []string{"", "view", "FULL", "inspect"} {
		if panelMode(q) != "view" {
			t.Errorf("panelMode(%q) = %q, want view", q, panelMode(q))
		}
	}
	if panelMode("full") != "full" {
		t.Error(`panelMode("full") != full`)
	}
}
