package aimemory

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// Verbatim from `ai-memory 2.4.0 read-page --json` on 2026-09-25, body
// shortened. The "## Raw observations" heading is kept because it is the only
// surface wick has for a session's raw events — there is no CLI subcommand
// that lists them — so the Wiki tab reads them out of this body.
const pageFixture = `{
  "path": "sessions/339153b2-1369-4152-988c-cbe2ca6f3ea1.md",
  "workspace": "default",
  "project": "proj",
  "title": "Ingat: server produksi project uji ini bernama srv-melati-07",
  "body": "# Ingat\n\n## Session metadata\n\n- **session_id:** ` + "`339153b2`" + `\n\n## Raw observations\n\n- ` + "`session-start`" + ` @ 2026-09-24T15:26:56Z — session-start\n",
  "frontmatter": {"session_id": "339153b2-1369-4152-988c-cbe2ca6f3ea1", "agent": "claude-code", "tier": "episodic"}
}`

func TestReadPageUsesPathNotTheSearchForm(t *testing.T) {
	st := &stubRun{out: pageFixture}
	pg, err := newSource(st).ReadPage(context.Background(),
		agentmemory.Conn{DataDir: "/srv/mem/data"},
		agentmemory.ReadScope{Workspace: "default", Project: "proj"},
		"sessions/339153b2-1369-4152-988c-cbe2ca6f3ea1.md")
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	joined := strings.Join(st.args, " ")
	// --path, not the positional query: the positional form runs an FTS
	// search and returns the TOP HIT, so a path that no longer exists would
	// answer with a different page and look like a successful read.
	if !strings.Contains(joined, "--path sessions/339153b2-1369-4152-988c-cbe2ca6f3ea1.md") {
		t.Fatalf("read-page must address the page by --path, got %q", joined)
	}
	// The scope has to be explicit or the backend answers about whatever
	// project it resolves from elsewhere (PLAN §14).
	for _, want := range []string{"--workspace default", "--project proj", "--json"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if !strings.Contains(pg.Body, "## Raw observations") {
		t.Fatal("the session page's raw-observations section must survive the parse — it is the only source wick has for them")
	}
	var fm map[string]any
	if err := json.Unmarshal(pg.Frontmatter, &fm); err != nil {
		t.Fatalf("frontmatter must stay valid JSON: %v", err)
	}
	if fm["session_id"] != "339153b2-1369-4152-988c-cbe2ca6f3ea1" {
		t.Fatalf("frontmatter lost session_id: %v", fm)
	}
}

func TestReadPageRefusesAnEmptyPath(t *testing.T) {
	st := &stubRun{out: pageFixture}
	if _, err := newSource(st).ReadPage(context.Background(), agentmemory.Conn{}, agentmemory.ReadScope{}, "  "); err == nil {
		t.Fatal("an empty path must be refused here, not turned into a top-hit search")
	}
	if st.args != nil {
		t.Fatal("nothing should have been run")
	}
}

// Verbatim from `message list --json` on 2026-09-25. The sender is two UUIDs
// and no name — that is the whole document, not a trimmed one.
const messagesFixture = `[
  {
    "id": "01a0d641-cbd4-71d3-8998-8890cadf03f7",
    "to_workspace_id": "01a0d406-299b-7c31-a687-fc0590eede74",
    "to_project_id": "01a0d411-27a4-78d0-af37-2baeeaef8cc5",
    "from_workspace_id": "01a0d406-299b-7c31-a687-fc0590eede74",
    "from_project_id": "01a0d63f-cd05-7591-af8d-18e7d68ce8fa",
    "from_agent": "other",
    "from_owner_user": null,
    "subject": "wick 4C probe",
    "body": "shape verification only",
    "state": "pending",
    "created_at": "2026-09-25T01:50:31.380413Z",
    "claimed_at": null
  }
]`

func TestMessagesKeepsDataDirAfterTheTwoWordSubcommand(t *testing.T) {
	st := &stubRun{out: messagesFixture}
	msgs, err := newSource(st).Messages(context.Background(),
		agentmemory.Conn{DataDir: "/srv/mem/data"},
		agentmemory.ReadScope{Workspace: "default", Project: "proj2"}, "inbox", 50)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	// `message list` is TWO words. The shared args() helper inserts
	// --data-dir straight after the first token, which would land it between
	// them, so this path builds its own command — pin that.
	if len(st.args) < 2 || st.args[0] != "message" || st.args[1] != "list" {
		t.Fatalf("subcommand must stay contiguous, got %v", st.args)
	}
	if strings.Contains(strings.Join(st.args, " "), "--outbox") {
		t.Fatal("inbox must not ask for the outbox")
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	// The sender is an id and stays an id. No name for it exists in this
	// document, in the MCP equivalent, or in the project listing — inventing
	// one would tell an operator which project sent the message.
	if m.FromProjectID != "01a0d63f-cd05-7591-af8d-18e7d68ce8fa" {
		t.Fatalf("sender id lost: %+v", m)
	}
	if m.Subject != "wick 4C probe" || m.State != "pending" {
		t.Fatalf("message parsed wrong: %+v", m)
	}
	// claimed_at is null here; a nil pointer must land as "" rather than as
	// the string "<nil>".
	if m.ClaimedAt != "" {
		t.Fatalf("null claimed_at must be empty, got %q", m.ClaimedAt)
	}
}

func TestMessagesOutboxAsksForIt(t *testing.T) {
	st := &stubRun{out: "[]"}
	if _, err := newSource(st).Messages(context.Background(), agentmemory.Conn{},
		agentmemory.ReadScope{Workspace: "default", Project: "p"}, "outbox", 0); err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if !strings.Contains(strings.Join(st.args, " "), "--outbox") {
		t.Fatalf("outbox not requested: %v", st.args)
	}
}

// rpcOK wraps a tool payload in the two layers the transport really uses: a
// JSON-RPC result whose content[0].text is the payload as a JSON STRING.
func rpcOK(payload string) string {
	b, _ := json.Marshal(payload)
	return `{"jsonrpc":"2.0","id":1,"result":{"isError":false,"content":[{"type":"text","text":` + string(b) + `}]}}`
}

// callArgs digs the tool arguments out of a request recorded by mcpServer
// (declared in mcp_test.go — one fake daemon for the whole package).
func callArgs(t *testing.T, sent *map[string]any) (string, map[string]any) {
	t.Helper()
	params, _ := (*sent)["params"].(map[string]any)
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	return name, args
}

func TestCancelHandoffReportsAlreadyGoneAsSuchNotAsSuccess(t *testing.T) {
	// Verbatim from a second cancel of the same id against ai-memory 2.4.0:
	// no JSON-RPC error, isError false, cancelled false. The handoff had
	// already expired or been accepted.
	srv, _ := mcpServer(t, rpcOK(`{"handoff_id":"01a0d641","cancelled":false,"state":"expired"}`), http.StatusOK)
	res, err := source{}.CancelHandoff(context.Background(),
		agentmemory.Conn{BaseURL: srv.URL},
		agentmemory.ReadScope{Workspace: "default", Project: "scratch"}, "01a0d641")
	if err != nil {
		t.Fatalf("a successful call reporting nothing-to-cancel is not an error: %v", err)
	}
	if res.Cancelled {
		t.Fatal("cancelled must stay false — reporting it as a success tells the operator they stopped a baton that had already stopped itself")
	}
	if res.State != "expired" {
		t.Fatalf("state lost: %+v", res)
	}
}

func TestCancelHandoffPassesTheScopeAndNeverAnyOwner(t *testing.T) {
	srv, sent := mcpServer(t, rpcOK(`{"handoff_id":"abc","cancelled":true,"state":"expired"}`), http.StatusOK)
	res, err := source{}.CancelHandoff(context.Background(),
		agentmemory.Conn{BaseURL: srv.URL},
		agentmemory.ReadScope{Workspace: "default", Project: "proj2"}, "abc")
	if err != nil || !res.Cancelled {
		t.Fatalf("CancelHandoff: %v %+v", err, res)
	}
	name, args := callArgs(t, sent)
	if name != "memory_handoff_cancel" {
		t.Fatalf("wrong tool called: %v", name)
	}
	if args["workspace"] != "default" || args["project"] != "proj2" || args["handoff_id"] != "abc" {
		t.Fatalf("scope or id not sent: %v", args)
	}
	// any_owner would consume a baton belonging to an operator who is merely
	// away. That is a recovery action, not something a list's X button does.
	if _, ok := args["any_owner"]; ok {
		t.Fatalf("any_owner must never be sent: %v", args)
	}
}

func TestCancelHandoffNeedsAnExplicitScope(t *testing.T) {
	for _, sc := range []agentmemory.ReadScope{{}, {Workspace: "default"}, {Project: "p"}} {
		if _, err := (source{}).CancelHandoff(context.Background(), agentmemory.Conn{BaseURL: "http://127.0.0.1:1"}, sc, "abc"); err == nil {
			t.Fatalf("scope %+v must be refused: an unscoped cancel retires a baton in whichever project the daemon resolves", sc)
		}
	}
}

func TestForgetSweepDryRunIsTheDefaultShape(t *testing.T) {
	st := &stubRun{out: "Swept 0 pages; pruned 0 observations.\n"}
	rep, err := newSource(st).ForgetSweep(context.Background(),
		agentmemory.Conn{DataDir: "/srv/mem/data"}, agentmemory.ReadScope{Workspace: "default", Project: "p"}, true)
	if err != nil {
		t.Fatalf("ForgetSweep: %v", err)
	}
	joined := strings.Join(st.args, " ")
	if !strings.Contains(joined, "--dry-run") {
		t.Fatalf("preview must pass --dry-run: %q", joined)
	}
	// forget-sweep has no --json mode, so appending it would make the
	// command fail rather than produce structured output.
	if strings.Contains(joined, "--json") {
		t.Fatalf("forget-sweep rejects --json: %q", joined)
	}
	if !rep.DryRun || !strings.Contains(rep.Output, "pruned 0 observations") {
		t.Fatalf("report wrong: %+v", rep)
	}

	st2 := &stubRun{out: "done"}
	if _, err := newSource(st2).ForgetSweep(context.Background(), agentmemory.Conn{}, agentmemory.ReadScope{}, false); err != nil {
		t.Fatalf("ForgetSweep: %v", err)
	}
	if strings.Contains(strings.Join(st2.args, " "), "--dry-run") {
		t.Fatalf("a real sweep must not pass --dry-run: %v", st2.args)
	}
}
