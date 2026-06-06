package api

import "testing"

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"127.0.0.1:9425", true},
		{"127.0.0.1", true},
		{"[::1]:9425", true},
		{"::1", true},
		{"localhost:9425", true},
		{"localhost", true},
		{"LOCALHOST:9425", true},
		{"example.com:9425", false},
		{"wick.gung.web.id", false},
		{"8.8.8.8", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isLoopbackHost(c.in); got != c.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMCPLoopbackExempt(t *testing.T) {
	cases := []struct {
		path, host string
		want       bool
	}{
		{"/mcp", "127.0.0.1:9425", true}, // the internal agent path
		{"/mcp", "localhost:9425", true},
		{"/mcp", "[::1]:9425", true},
		{"/mcp", "example.com:9425", false}, // non-loopback → allowlist still applies
		{"/other", "127.0.0.1:9425", false}, // scoped to /mcp ONLY
		{"/health", "127.0.0.1", false},     // /health is handled separately
		{"/mcp", "", false},
	}
	for _, c := range cases {
		if got := mcpLoopbackExempt(c.path, c.host); got != c.want {
			t.Errorf("mcpLoopbackExempt(%q,%q) = %v, want %v", c.path, c.host, got, c.want)
		}
	}
}
