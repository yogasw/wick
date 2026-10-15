//go:build browser_integration

// Integration test for the moli engine: task ops (moli serve started per call
// and attached over CDP) and a live session (detached moli serve on a named
// profile, reconnected per op, cookies surviving a close + reopen).
//
//	Run: MOLI_BINARY=$HOME/.local/bin/moli \
//	       go test -tags browser_integration ./connector/playwright_browser/ -run TestIntegrationMoli
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func moliIntegrationCfg(t *testing.T) map[string]string {
	t.Helper()
	bin := strings.TrimSpace(os.Getenv("MOLI_BINARY"))
	if bin == "" {
		t.Skip("MOLI_BINARY not set — skipping moli integration test")
	}
	return map[string]string{"browser": "moli", "moli_executable_path": bin, "session_dir": t.TempDir()}
}

func moliOp(t *testing.T, cfg, in map[string]string, fn func(*connector.Ctx) (any, error)) map[string]any {
	t.Helper()
	res, err := fn(connector.NewPluginCtx(context.Background(), cfg, in))
	if err != nil {
		t.Fatalf("op failed: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", res)
	}
	return m
}

func TestIntegrationMoliTaskOps(t *testing.T) {
	cfg := moliIntegrationCfg(t)

	got := moliOp(t, cfg, map[string]string{"url": "https://example.com", "as_text": "true"}, getContent)
	if !strings.Contains(got["content"].(string), "documentation examples") {
		t.Fatalf("get_content: %v", got)
	}
	md := moliOp(t, cfg, map[string]string{"url": "https://example.com"}, getMarkdown)
	if !strings.Contains(md["content"].(string), "documentation examples") || md["format"] != "markdown" {
		t.Fatalf("get_markdown: %v", md)
	}
	ev := moliOp(t, cfg, map[string]string{"url": "https://example.com", "script": "document.title"}, evalJS)
	if !strings.Contains(strings.ToLower(toString(ev["result"])), "example domain") {
		t.Fatalf("eval: %v", ev)
	}
	shot := moliOp(t, cfg, map[string]string{"url": "https://example.com", "full_page": "true"}, screenshot)
	t.Logf("screenshot keys: %v", mapKeys(shot))
	pdf := moliOp(t, cfg, map[string]string{"url": "https://example.com"}, renderPDF)
	t.Logf("pdf keys: %v", mapKeys(pdf))
	run := moliOp(t, cfg, map[string]string{"actions": `[
		{"action":"goto","url":"https://example.com"},
		{"action":"click","selector":"a"},
		{"action":"wait_for_load_state"},
		{"action":"eval","script":"location.host"}
	]`}, run)
	t.Logf("run: %v", run)
}

func TestIntegrationMoliLiveSession(t *testing.T) {
	cfg := moliIntegrationCfg(t)
	open := func() string {
		m := moliOp(t, cfg, map[string]string{"profile": "moli-it"}, sessionOpen)
		return m["session_id"].(string)
	}
	sid := open()
	moliOp(t, cfg, map[string]string{"session_id": sid, "actions": `[
		{"action":"goto","url":"https://httpbin.org/cookies/set?wick=moli"}
	]`}, run)
	list := moliOp(t, cfg, map[string]string{}, sessionListOp)
	if list["count"].(int) != 1 {
		t.Fatalf("session_list: %v", list)
	}
	moliOp(t, cfg, map[string]string{"session_id": sid}, sessionCloseOp)

	// Reopen the same named profile: the cookie must have persisted.
	sid = open()
	defer moliOp(t, cfg, map[string]string{"session_id": sid}, sessionCloseOp)
	res := moliOp(t, cfg, map[string]string{"session_id": sid, "actions": `[
		{"action":"goto","url":"https://httpbin.org/cookies"},
		{"action":"content","selector":"body"}
	]`}, run)
	if !strings.Contains(toString(res), "moli") {
		t.Fatalf("cookie did not persist across sessions: %v", res)
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
