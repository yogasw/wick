package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// adminCtx returns a context with a synthetic local-admin user — the
// same identity RunMCPStdio injects for stdio sessions.
func adminCtx() context.Context {
	return login.WithUser(context.Background(), &entity.User{ID: "local", Role: entity.RoleAdmin}, nil)
}

// ── dispatchLine ─────────────────────────────────────────────────────

func TestDispatchLineInitialize(t *testing.T) {
	h := &Handler{}
	msg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`
	got := h.dispatchLine(adminCtx(), []byte(msg))

	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  initializeResult `json:"result"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, got)
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc = %q, want 2.0", resp.JSONRPC)
	}
	if resp.ID != 1 {
		t.Fatalf("id = %d, want 1", resp.ID)
	}
	if resp.Result.ProtocolVersion != "2025-03-26" {
		t.Fatalf("protocolVersion = %q, want 2025-03-26", resp.Result.ProtocolVersion)
	}
	if resp.Result.ServerInfo.Name != "wick" {
		t.Fatalf("serverInfo.name = %q, want wick", resp.Result.ServerInfo.Name)
	}
}

func TestDispatchLineToolsList(t *testing.T) {
	h := &Handler{}
	msg := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	got := h.dispatchLine(adminCtx(), []byte(msg))

	var resp struct {
		Result toolListResult `json:"result"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, got)
	}
	if len(resp.Result.Tools) != 4 {
		t.Fatalf("tools count = %d, want 4", len(resp.Result.Tools))
	}
	names := make([]string, len(resp.Result.Tools))
	for i, tool := range resp.Result.Tools {
		names[i] = tool.Name
	}
	for _, want := range []string{"wick_list", "wick_search", "wick_get", "wick_execute"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing tool %q in %v", want, names)
		}
	}
}

func TestDispatchLineUnknownMethod(t *testing.T) {
	h := &Handler{}
	msg := `{"jsonrpc":"2.0","id":3,"method":"unknown/method"}`
	got := h.dispatchLine(adminCtx(), []byte(msg))

	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, got)
	}
	if resp.Error == nil {
		t.Fatal("expected error field, got nil")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Fatalf("error.code = %d, want %d (MethodNotFound)", resp.Error.Code, errMethodNotFound)
	}
}

func TestDispatchLineInvalidJSON(t *testing.T) {
	h := &Handler{}
	got := h.dispatchLine(adminCtx(), []byte(`{not valid json`))

	var resp struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, got)
	}
	if resp.Error == nil {
		t.Fatal("expected error field, got nil")
	}
	if resp.Error.Code != errParseError {
		t.Fatalf("error.code = %d, want %d (ParseError)", resp.Error.Code, errParseError)
	}
}

func TestDispatchLinePing(t *testing.T) {
	h := &Handler{}
	msg := `{"jsonrpc":"2.0","id":4,"method":"ping"}`
	got := h.dispatchLine(adminCtx(), []byte(msg))

	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, got)
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc = %q", resp.JSONRPC)
	}
}

// Notifications have no "id" field; the HTTP handler returns 202 with
// no body. dispatchLine should return empty bytes (nothing to write).
func TestDispatchLineNotificationReturnsEmpty(t *testing.T) {
	h := &Handler{}
	msg := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	got := h.dispatchLine(adminCtx(), []byte(msg))

	trimmed := bytes.TrimSpace(got)
	if len(trimmed) != 0 {
		t.Fatalf("notification: want empty response, got %s", got)
	}
}

// ── ServeStdio ───────────────────────────────────────────────────────

func TestServeStdioMultipleMessages(t *testing.T) {
	h := &Handler{}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
	}, "\n")

	r := strings.NewReader(input)
	var w bytes.Buffer
	h.ServeStdio(adminCtx(), r, &w)

	lines := strings.Split(strings.TrimSpace(w.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 response lines, got %d:\n%s", len(lines), w.String())
	}

	// Each line must be valid JSON with jsonrpc:2.0
	for i, line := range lines {
		var env struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d: unmarshal: %v\n%s", i+1, err, line)
		}
		if env.JSONRPC != "2.0" {
			t.Fatalf("line %d: jsonrpc = %q, want 2.0", i+1, env.JSONRPC)
		}
	}
}

func TestServeStdioSkipsBlankLines(t *testing.T) {
	h := &Handler{}
	input := "\n\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n\n"

	r := strings.NewReader(input)
	var w bytes.Buffer
	h.ServeStdio(adminCtx(), r, &w)

	lines := strings.Split(strings.TrimSpace(w.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 response line (blank lines skipped), got %d:\n%s", len(lines), w.String())
	}
}

func TestServeStdioNotificationProducesNoLine(t *testing.T) {
	h := &Handler{}
	// notification + ping: only ping gets a response line
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
	}, "\n")

	r := strings.NewReader(input)
	var w bytes.Buffer
	h.ServeStdio(adminCtx(), r, &w)

	lines := strings.Split(strings.TrimSpace(w.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 response line, got %d:\n%s", len(lines), w.String())
	}
}

func TestServeStdioIDsMatch(t *testing.T) {
	h := &Handler{}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":10,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":20,"method":"ping"}`,
	}, "\n")

	r := strings.NewReader(input)
	var w bytes.Buffer
	h.ServeStdio(adminCtx(), r, &w)

	lines := strings.Split(strings.TrimSpace(w.String()), "\n")
	ids := []int{10, 20}
	for i, line := range lines {
		var env struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d unmarshal: %v", i, err)
		}
		if env.ID != ids[i] {
			t.Fatalf("line %d: id = %d, want %d", i, env.ID, ids[i])
		}
	}
}
