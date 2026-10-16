package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp/handlers"
)

// dispatchToolAs runs one tools/call as the given user and returns the text
// payload plus the isError flag.
func dispatchToolAs(t *testing.T, h *Handler, user *entity.User, name string, args map[string]any) (string, bool) {
	t.Helper()
	p, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, p)
	raw := h.dispatchLine(login.WithUser(context.Background(), user, nil), []byte(msg))
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("no content in %s response:\n%s", name, raw)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

// listAs is dispatchToolAs for wick_list, decoded.
func listAs(t *testing.T, h *Handler, user *entity.User, args map[string]any) listResult {
	t.Helper()
	text, isErr := dispatchToolAs(t, h, user, "wick_list", args)
	if isErr {
		t.Fatalf("wick_list isError: %s", text)
	}
	var p listResult
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		t.Fatalf("unmarshal payload: %v\ntext=%s", err, text)
	}
	return p
}

// mentions reports whether a wick_list payload has the instance or one of
// its accounts.
func mentions(p listResult, rowID string) bool {
	for _, c := range p.Connectors {
		if c.ID == rowID || c.ParentID == rowID {
			return true
		}
	}
	return false
}

// hidingUser is a plain user who hid the given instance ids for themselves.
func hidingUser(id string, hidden ...string) *entity.User {
	return &entity.User{ID: id, Role: entity.RoleUser, Metadata: entity.UserMetadata{HiddenConnectors: hidden}}
}

// A user who hid an instance does not list it (nor their account on it) and
// gets a count + note; another user still lists it; include_hidden brings it
// back; a stale id in the hidden list never inflates the count.
func TestWickListSkipsHiddenForCaller(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, ssoStubModule())
	rows, err := svc.ListByKey(context.Background(), "sso-stub")
	if err != nil || len(rows) == 0 {
		t.Fatalf("bootstrap row missing: %v", err)
	}
	row := rows[0]
	ctx := context.Background()
	if err := svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if err := svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-a"); err != nil {
		t.Fatalf("save alice: %v", err)
	}
	h := NewHandler(svc)
	alice := hidingUser("u-alice", row.ID, "gone-instance")

	got := listAs(t, h, alice, map[string]any{})
	if mentions(got, row.ID) {
		t.Fatalf("alice hid %s but still lists it: %+v", row.ID, got.Connectors)
	}
	if got.HiddenCount != 1 {
		t.Fatalf("hidden_count = %d, want 1 (stale id must not count)", got.HiddenCount)
	}
	if want := "1 connector(s) hidden by you — pass include_hidden:true to see them"; got.HiddenNote != want {
		t.Fatalf("hidden_note = %q, want %q", got.HiddenNote, want)
	}
	if got.TotalConnectors != len(got.Connectors) {
		t.Fatalf("total_connectors = %d, want %d (after filter)", got.TotalConnectors, len(got.Connectors))
	}

	bob := listAs(t, h, &entity.User{ID: "u-bob", Role: entity.RoleUser}, map[string]any{})
	if !mentions(bob, row.ID) || bob.HiddenCount != 0 || bob.HiddenNote != "" {
		t.Fatalf("bob hid nothing and should list %s without a note: %+v", row.ID, bob)
	}

	for _, v := range []any{true, "true"} {
		all := listAs(t, h, alice, map[string]any{"include_hidden": v})
		if !mentions(all, row.ID) {
			t.Fatalf("include_hidden=%v should list %s", v, row.ID)
		}
		if got := accountNames(all); len(got) != 1 || got[0] != "SSO Stub – @alice" {
			t.Fatalf("include_hidden=%v should bring back alice's account, got %v", v, got)
		}
		if all.HiddenCount != 0 || all.HiddenNote != "" {
			t.Fatalf("include_hidden=%v should carry no hidden_count, got %d %q", v, all.HiddenCount, all.HiddenNote)
		}
	}

	// The raw JSON omits the fields when nothing was hidden.
	text, _ := dispatchToolAs(t, h, &entity.User{ID: "u-bob", Role: entity.RoleUser}, "wick_list", map[string]any{})
	if strings.Contains(text, "hidden_count") || strings.Contains(text, "hidden_note") {
		t.Fatalf("hidden fields should be omitted when zero: %s", text)
	}
}

// wick_search skips a hidden instance and counts it only when it matched.
func TestWickSearchSkipsHiddenMatches(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	rows, err := svc.ListByKey(context.Background(), "stub")
	if err != nil || len(rows) == 0 {
		t.Fatalf("bootstrap row missing: %v", err)
	}
	h := NewHandler(svc)
	alice := hidingUser("u-alice", rows[0].ID)

	type result struct {
		Total       int    `json:"total"`
		HiddenCount int    `json:"hidden_count"`
		HiddenNote  string `json:"hidden_note"`
	}
	search := func(args map[string]any) result {
		t.Helper()
		text, isErr := dispatchToolAs(t, h, alice, "wick_search", args)
		if isErr {
			t.Fatalf("wick_search isError: %s", text)
		}
		var r result
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			t.Fatalf("unmarshal: %v\ntext=%s", err, text)
		}
		return r
	}

	if r := search(map[string]any{"query": "echo"}); r.Total != 0 || r.HiddenCount != 1 || r.HiddenNote == "" {
		t.Fatalf("matching hidden instance: got %+v, want total 0 + hidden_count 1 + note", r)
	}
	if r := search(map[string]any{"query": "no-such-thing"}); r.HiddenCount != 0 || r.HiddenNote != "" {
		t.Fatalf("non-matching hidden instance must not count: %+v", r)
	}
	if r := search(map[string]any{"query": "echo", "include_hidden": true}); r.Total != 1 || r.HiddenCount != 0 {
		t.Fatalf("include_hidden should search it: %+v", r)
	}
}

// Hiding is display-only: wick_get by id still resolves a hidden instance.
func TestWickGetIgnoresHidden(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	rows, err := svc.ListByKey(context.Background(), "stub")
	if err != nil || len(rows) == 0 {
		t.Fatalf("bootstrap row missing: %v", err)
	}
	h := NewHandler(svc)
	text, isErr := dispatchToolAs(t, h, hidingUser("u-alice", rows[0].ID), "wick_get", map[string]any{"id": rows[0].ID})
	if isErr || !strings.Contains(text, rows[0].ID) {
		t.Fatalf("wick_get on a hidden instance should still work, isError=%v: %s", isErr, text)
	}
}

func TestListAndSearchSchemasOfferIncludeHidden(t *testing.T) {
	for _, d := range handlers.MetaToolDescriptors() {
		if d.Name != "wick_list" && d.Name != "wick_search" {
			continue
		}
		schema, _ := d.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["include_hidden"]; !ok {
			t.Errorf("%s schema has no include_hidden: %v", d.Name, props)
		}
		if !strings.Contains(d.Description, "include_hidden") {
			t.Errorf("%s description does not mention include_hidden", d.Name)
		}
	}
}
