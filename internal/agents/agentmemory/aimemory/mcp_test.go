package aimemory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// briefingFixture is a verbatim capture of the daemon's reply for
// default/proj2 (ai-memory 2.4.0, 2026-09-25), recent_pages cut to two rows.
// The point of keeping it whole is the shape that matters here: the payload
// is a JSON document inside a STRING inside the JSON-RPC result.
const briefingFixture = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\n  \"counts\": {\n    \"pages_latest\": 6,\n    \"pages_all\": 15,\n    \"sessions\": 6,\n    \"observations\": 172,\n    \"evidence_rows\": 0\n  },\n  \"activity_7d\": {\n    \"days\": 7,\n    \"sessions\": 6,\n    \"observations\": 172,\n    \"pages_updated\": 6\n  },\n  \"activity_30d\": {\n    \"days\": 30,\n    \"sessions\": 2,\n    \"observations\": 40,\n    \"pages_updated\": 3\n  },\n  \"last_observation_at\": \"2026-09-24T17:04:40.437113Z\",\n  \"pending_handoff_count\": 2,\n  \"pending_message_count\": 0,\n  \"rules\": [],\n  \"slots\": [],\n  \"recent_pages\": [\n    {\n      \"path\": \"sessions/b047548c-7c9f-45c2-b96e-b5d7aeaa36db.md\",\n      \"title\": \"Panggil tool memory_status dari MCP ai-memory\",\n      \"kind\": \"session\",\n      \"updated_at\": \"2026-09-24T17:04:40.440131Z\"\n    },\n    {\n      \"path\": \"sessions/227a6362-b122-476c-be77-40a707aa547b.md\",\n      \"title\": \"Kamu agent BARU di project ini\",\n      \"kind\": \"session\",\n      \"updated_at\": \"2026-09-24T17:04:39.641325Z\"\n    }\n  ],\n  \"cross_project_dependents\": 0,\n  \"cross_project_dependencies\": 0\n}"}],"isError":false}}`

// emptyProjectFixture is the same call against a project that has never been
// written to — every counter is a real zero and last_observation_at is null.
const emptyProjectFixture = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"counts\":{\"pages_latest\":0,\"pages_all\":0,\"sessions\":0,\"observations\":0,\"evidence_rows\":0},\"activity_7d\":{\"days\":7,\"sessions\":0,\"observations\":0,\"pages_updated\":0},\"activity_30d\":{\"days\":30,\"sessions\":0,\"observations\":0,\"pages_updated\":0},\"last_observation_at\":null,\"pending_handoff_count\":0,\"pending_message_count\":0,\"rules\":[],\"slots\":[],\"recent_pages\":[],\"cross_project_dependents\":0,\"cross_project_dependencies\":0}"}],"isError":false}}`

// mcpServer stands in for the daemon's /mcp endpoint and records the request
// body, so the tests can assert what wick actually asked for.
func mcpServer(t *testing.T, reply string, status int) (*httptest.Server, *map[string]any) {
	t.Helper()
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != mcpPath {
			t.Errorf("path: %q, want %q", r.URL.Path, mcpPath)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sent)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &sent
}

func briefingFor(t *testing.T, srv *httptest.Server, ws, project string) (*agentmemory.ProjectBriefing, error) {
	t.Helper()
	return source{}.ProjectBriefing(context.Background(), agentmemory.Conn{BaseURL: srv.URL, WebEnabled: true},
		agentmemory.ReadScope{Workspace: ws, Project: project})
}

// TestBriefingParsesBothLayers is the shape check this file exists for: the
// JSON-RPC envelope AND the JSON-in-a-string payload inside it. Parsing only
// the outer layer would leave every counter at zero and look like an empty
// project.
func TestBriefingParsesBothLayers(t *testing.T) {
	srv, _ := mcpServer(t, briefingFixture, http.StatusOK)

	b, err := briefingFor(t, srv, "default", "proj2")
	if err != nil {
		t.Fatalf("ProjectBriefing: %v", err)
	}
	if b.Counts.Sessions != 6 || b.Counts.Observations != 172 || b.Counts.PagesLatest != 6 || b.Counts.PagesAll != 15 {
		t.Fatalf("counts: %+v", b.Counts)
	}
	// 7d and 30d are read separately on purpose: a fixture where they are
	// equal would not catch the two being wired to the same field.
	if b.Activity7d.Sessions != 6 || b.Activity30d.Sessions != 2 || b.Activity30d.Observations != 40 {
		t.Fatalf("activity: 7d=%+v 30d=%+v", b.Activity7d, b.Activity30d)
	}
	if b.PendingHandoffs != 2 || b.LastObservationAt != "2026-09-24T17:04:40.437113Z" {
		t.Fatalf("handoffs=%d last=%q", b.PendingHandoffs, b.LastObservationAt)
	}
	if len(b.RecentPages) != 2 || b.RecentPages[0].Kind != "session" ||
		b.RecentPages[0].Path != "sessions/b047548c-7c9f-45c2-b96e-b5d7aeaa36db.md" {
		t.Fatalf("recent pages: %+v", b.RecentPages)
	}
}

// TestBriefingSendsExplicitScope: the scope has to be IN the call. ai-memory
// resolves an unnamed scope from the caller's cwd and a plain HTTP POST
// carries none, so a briefing without workspace+project silently describes
// some other project (verified 2026-09-25: two directories, identical reply).
func TestBriefingSendsExplicitScope(t *testing.T) {
	srv, sent := mcpServer(t, briefingFixture, http.StatusOK)

	if _, err := briefingFor(t, srv, "default", "proj2"); err != nil {
		t.Fatalf("ProjectBriefing: %v", err)
	}
	if (*sent)["method"] != "tools/call" {
		t.Fatalf("method: %v", (*sent)["method"])
	}
	params, _ := (*sent)["params"].(map[string]any)
	if params["name"] != "memory_briefing" {
		t.Fatalf("tool: %v — this dashboard calls exactly one MCP tool", params["name"])
	}
	args, _ := params["arguments"].(map[string]any)
	if args["workspace"] != "default" || args["project"] != "proj2" {
		t.Fatalf("arguments: %+v", args)
	}
}

// TestBriefingRefusesUnscopedCall: an empty workspace or project must fail
// here, before the request, rather than come back as another project's
// numbers under this project's name.
func TestBriefingRefusesUnscopedCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(briefingFixture))
	}))
	defer srv.Close()

	unscoped := []agentmemory.ReadScope{
		{},
		{Workspace: "default"},
		{Project: "proj2"},
		{Workspace: " ", Project: "proj2"},
	}
	src := source{}
	for _, sc := range unscoped {
		if _, err := src.ProjectBriefing(context.Background(), agentmemory.Conn{BaseURL: srv.URL}, sc); err == nil {
			t.Fatalf("scope %+v was accepted", sc)
		}
	}
	if called {
		t.Fatal("an unscoped briefing reached the daemon")
	}
}

// TestBriefingRPCError: an unknown project answers a JSON-RPC error with HTTP
// 200. Reading only the status would turn that into a zeroed briefing.
func TestBriefingRPCError(t *testing.T) {
	srv, _ := mcpServer(t, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"project 'nope' not found in workspace 'default'"}}`, http.StatusOK)

	b, err := briefingFor(t, srv, "default", "nope")
	if err == nil {
		t.Fatalf("want an error, got %+v", b)
	}
	if !strings.Contains(err.Error(), "not found in workspace") {
		t.Fatalf("the backend's own message must survive: %v", err)
	}
}

// TestBriefingToolError: the other failure mode — a result flagged isError
// with the message in its text block.
func TestBriefingToolError(t *testing.T) {
	srv, _ := mcpServer(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"store is locked"}],"isError":true}}`, http.StatusOK)

	if _, err := briefingFor(t, srv, "default", "proj2"); err == nil || !strings.Contains(err.Error(), "store is locked") {
		t.Fatalf("want the tool's message, got %v", err)
	}
}

// TestBriefingDaemonDown: nothing listening. It has to be an error, not an
// empty briefing — the Projects tab decides between "no numbers" and "zero"
// on exactly this.
func TestBriefingDaemonDown(t *testing.T) {
	_, err := source{}.ProjectBriefing(context.Background(),
		agentmemory.Conn{BaseURL: "http://127.0.0.1:1"},
		agentmemory.ReadScope{Workspace: "default", Project: "proj2"})
	if err == nil {
		t.Fatal("a dead daemon answered a briefing")
	}
}

// TestBriefingHTTPError covers a transport-level refusal (auth, wrong mount).
func TestBriefingHTTPError(t *testing.T) {
	srv, _ := mcpServer(t, "missing bearer token", http.StatusUnauthorized)

	if _, err := briefingFor(t, srv, "default", "proj2"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want the status carried through, got %v", err)
	}
}

// TestBriefingGarbage: a body that is not JSON-RPC at all (a proxy's error
// page) must fail loudly rather than decode to zeros.
func TestBriefingGarbage(t *testing.T) {
	srv, _ := mcpServer(t, "<html>bad gateway</html>", http.StatusOK)

	if _, err := briefingFor(t, srv, "default", "proj2"); err == nil {
		t.Fatal("an HTML body parsed as a briefing")
	}
}

// TestBriefingEmptyProject: every counter zero is a FACT here, not a failure.
// A project that exists and has never been written to must come back as a
// briefing, so the tab shows 0 rather than "unavailable".
func TestBriefingEmptyProject(t *testing.T) {
	srv, _ := mcpServer(t, emptyProjectFixture, http.StatusOK)

	b, err := briefingFor(t, srv, "default", "scratch")
	if err != nil {
		t.Fatalf("ProjectBriefing: %v", err)
	}
	if b.Counts.Sessions != 0 || b.LastObservationAt != "" || len(b.RecentPages) != 0 {
		t.Fatalf("briefing: %+v", b)
	}
}

// TestBriefingSSEBody: the streamable-HTTP transport is allowed to answer as
// an event stream, and wick advertises that it accepts one. ai-memory answers
// plain JSON today, so this pins the fallback rather than the current
// behaviour.
func TestBriefingSSEBody(t *testing.T) {
	srv, _ := mcpServer(t, "event: message\ndata: "+emptyProjectFixture+"\n\n", http.StatusOK)

	if _, err := briefingFor(t, srv, "default", "scratch"); err != nil {
		t.Fatalf("an SSE-framed reply must parse: %v", err)
	}
}

// TestSourceIsBriefer locks the wiring: the core only fans out briefings when
// the backend implements ProjectBriefer, so losing the method would quietly
// empty three columns instead of failing a build.
func TestSourceIsBriefer(t *testing.T) {
	var _ agentmemory.ProjectBriefer = source{}
}
