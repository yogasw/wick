package airouter

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestIdentityProbeDistinguishesRouters verifies that an externally-started
// process on a router's port is only reported "running" for the router whose
// manifest identity actually matches — the fix for two routers sharing the
// default port 20128.
func TestIdentityProbeDistinguishesRouters(t *testing.T) {
	// Fake backend serving 9router's web manifest.
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		_, _ = io.WriteString(w, `{"name":"9Router - AI Infrastructure Management","short_name":"9Router"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	// Manager expecting "9router" → matches the served manifest.
	m9 := newManager(Descriptor{ID: "t9", IdentitySubstr: "9router"})
	m9.port.Store(int32(port))
	if !m9.identityMatches() {
		t.Fatal("expected 9router identity to match its own manifest")
	}
	if !m9.runningNow() {
		t.Fatal("runningNow should be true for a matching external process")
	}

	// Manager expecting "omniroute" → the SAME 9router backend must NOT match,
	// so its tile stays "stopped" even though the port answers.
	mo := newManager(Descriptor{ID: "to", IdentitySubstr: "omniroute"})
	mo.port.Store(int32(port))
	if mo.identityMatches() {
		t.Fatal("omniroute must not match a 9router manifest")
	}
	if mo.runningNow() {
		t.Fatal("runningNow should be false — port answers but identity mismatches")
	}

	// No signature configured → port-only, accept.
	mn := newManager(Descriptor{ID: "tn"})
	mn.port.Store(int32(port))
	if !mn.identityMatches() {
		t.Fatal("empty IdentitySubstr should accept (degrade to port-only)")
	}
}
