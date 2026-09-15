package api

import "testing"

// The CLI channel is used by scripts on this machine, which know
// 127.0.0.1 and not the public name the host allowlist is written in.
// Without the exemption the channel is refused before the token is even
// read — which is how it shipped the first time.
func TestCLILoopbackExempt(t *testing.T) {
	for _, tc := range []struct {
		path, host string
		want       bool
	}{
		{"/api/cli/send", "127.0.0.1:9425", true},
		{"/api/cli/whoami", "localhost:9425", true},
		{"/api/cli/send", "[::1]:9425", true},

		// A remote caller gets no exemption: it must use the public name,
		// which the allowlist then checks as usual.
		{"/api/cli/send", "support-assistant.qiscus.io", false},
		{"/api/cli/send", "10.0.0.5:9425", false},

		// …and the exemption covers nothing but this subtree, so a
		// loopback request cannot walk into the rest of the app with it.
		{"/api/tickets", "127.0.0.1:9425", false},
		{"/tools/agents/sessions", "127.0.0.1:9425", false},
		{"/api/clifford", "127.0.0.1:9425", false},
		{"/api/cli", "127.0.0.1:9425", false},
	} {
		if got := cliLoopbackExempt(tc.path, tc.host); got != tc.want {
			t.Errorf("cliLoopbackExempt(%q, %q) = %v, want %v", tc.path, tc.host, got, tc.want)
		}
	}
}
