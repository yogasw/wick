package agents

import (
	"net/http"
	"testing"
)

// A handshake that reaches us through a proxy which rewrote Connection
// must still be upgradable; one that is not a websocket request must be
// left exactly as it came in.
func TestRestoreWSUpgradeHeader(t *testing.T) {
	cases := []struct {
		name       string
		upgrade    string
		connection []string
		want       string
	}{
		{"proxy rewrote connection", "websocket", []string{"keep-alive"}, "Upgrade"},
		{"connection dropped entirely", "websocket", nil, "Upgrade"},
		{"already correct is untouched", "websocket", []string{"Upgrade"}, "Upgrade"},
		{"token among others is untouched", "websocket", []string{"keep-alive, Upgrade"}, "keep-alive, Upgrade"},
		{"lowercase token is untouched", "websocket", []string{"upgrade"}, "upgrade"},
		{"plain request is untouched", "", []string{"keep-alive"}, "keep-alive"},
		{"other protocol is untouched", "h2c", []string{"keep-alive"}, "keep-alive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.upgrade != "" {
				h.Set("Upgrade", tc.upgrade)
			}
			for _, v := range tc.connection {
				h.Add("Connection", v)
			}
			restoreWSUpgradeHeader(h)
			if got := h.Get("Connection"); got != tc.want {
				t.Fatalf("Connection = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHeaderHasToken(t *testing.T) {
	h := http.Header{"Connection": []string{"keep-alive, Upgrade"}}
	if !headerHasToken(h, "Connection", "upgrade") {
		t.Error("token in a comma list should be found, case-insensitively")
	}
	if headerHasToken(h, "Connection", "close") {
		t.Error("absent token should not be found")
	}
	if headerHasToken(http.Header{}, "Connection", "upgrade") {
		t.Error("missing header should not report a token")
	}
}
