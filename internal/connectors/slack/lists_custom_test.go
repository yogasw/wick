package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/pkg/connector"
)

// newCustomCtx builds a ctx carrying both the custom-API policy configs
// and the per-call inputs, which is the combination every custom_api_call
// test needs.
func newCustomCtx(t *testing.T, configs, input map[string]string) *connector.Ctx {
	t.Helper()
	full := map[string]string{"auth_mode": "bot_token", "bot_token": "xoxb-test"}
	for k, v := range configs {
		full[k] = v
	}
	return connector.NewCtx(t.Context(), "test-row", full, input, http.DefaultClient, nil, nil)
}

// recordingSlack captures the method path, HTTP verb, query, and decoded
// body of the request it receives, then replies with a fixed payload.
type recordingSlack struct {
	path  string
	verb  string
	query url.Values
	body  map[string]any
}

func newRecordingSlack(t *testing.T, reply string) (*httptest.Server, *recordingSlack) {
	t.Helper()
	rec := &recordingSlack{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.path = r.URL.Path
		rec.verb = r.Method
		rec.query = r.URL.Query()
		_ = json.NewDecoder(r.Body).Decode(&rec.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// ── Lists ────────────────────────────────────────────────────────────

func TestShapeListsList_KeepsListFieldsDropsFileNoise(t *testing.T) {
	raw := map[string]any{
		"ok": true,
		"files": []any{
			map[string]any{
				"id": "F1234567", "name": "Sprint Board", "user": "U02ABCDEF",
				"created": float64(1758744346), "permalink": "https://x.slack.com/lists/F1234567",
				// file-only noise that must not survive
				"mimetype": "application/vnd.slack-list", "size": float64(4096), "thumb_64": "https://x/t.png",
			},
		},
		"paging": map[string]any{"count": float64(1), "total": float64(1), "page": float64(1), "pages": float64(1)},
	}
	out := shapeListsList(raw)

	lists := out["lists"].([]any)
	require.Len(t, lists, 1)
	entry := lists[0].(map[string]any)
	assert.Equal(t, "F1234567", entry["id"])
	assert.Equal(t, "Sprint Board", entry["name"])
	assert.Equal(t, "U02ABCDEF", entry["user"])
	assert.NotContains(t, entry, "mimetype")
	assert.NotContains(t, entry, "size")
	assert.NotContains(t, entry, "thumb_64")
	assert.Contains(t, out, "paging")
}

func TestShapeListsList_EmptyAndMalformedYieldEmptyArray(t *testing.T) {
	// A workspace with no Lists (or a free plan) still returns a usable
	// shape rather than a nil the caller has to guard.
	for name, raw := range map[string]any{
		"no files key": map[string]any{"ok": true},
		"not a map":    "unexpected",
		"nil":          nil,
	} {
		t.Run(name, func(t *testing.T) {
			out := shapeListsList(raw)
			assert.Equal(t, []any{}, out["lists"])
		})
	}
}

func TestListLists_UsesFilesListWithListsType(t *testing.T) {
	// Slack ships no slackLists.list — discovery must ride on files.list.
	srv, rec := newRecordingSlack(t, `{"ok":true,"files":[],"paging":{"pages":1}}`)
	withBaseURL(t, srv.URL)

	_, err := listLists(newCtxWithInput(t, map[string]string{"channel": "C12345", "limit": "50"}))
	require.NoError(t, err)

	assert.Equal(t, "/files.list", rec.path)
	assert.Equal(t, http.MethodGet, rec.verb)
	assert.Equal(t, "lists", rec.query.Get("types"))
	assert.Equal(t, "C12345", rec.query.Get("channel"))
	assert.Equal(t, "50", rec.query.Get("count"))
	assert.Equal(t, "1", rec.query.Get("page"))
}

func TestListListItems_SendsCursorAndArchived(t *testing.T) {
	srv, rec := newRecordingSlack(t, `{"ok":true,"items":[]}`)
	withBaseURL(t, srv.URL)

	_, err := listListItems(newCtxWithInput(t, map[string]string{
		"list_id": "F1234567", "cursor": "abc", "archived": "true",
	}))
	require.NoError(t, err)

	assert.Equal(t, "/slackLists.items.list", rec.path)
	assert.Equal(t, http.MethodPost, rec.verb)
	assert.Equal(t, "F1234567", rec.body["list_id"])
	assert.Equal(t, "abc", rec.body["cursor"])
	assert.Equal(t, true, rec.body["archived"])
}

func TestListOps_RequireTheirIDs(t *testing.T) {
	cases := map[string]struct {
		fn    func(*connector.Ctx) (any, error)
		input map[string]string
		want  string
	}{
		"get_list needs list_id":          {getList, map[string]string{}, "list_id is required"},
		"list items needs list_id":        {listListItems, map[string]string{}, "list_id is required"},
		"get item needs item_id":          {getListItem, map[string]string{"list_id": "F1"}, "item_id is required"},
		"delete item needs item_id":       {deleteListItem, map[string]string{"list_id": "F1"}, "item_id is required"},
		"create list needs name":          {createList, map[string]string{}, "name is required"},
		"create item needs list_id":       {createListItem, map[string]string{}, "list_id is required"},
		"update item needs cells":         {updateListItem, map[string]string{"list_id": "F1"}, "cells is required"},
		"update item rejects empty cells": {updateListItem, map[string]string{"list_id": "F1", "cells": "[]"}, "at least one cell"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// No base URL override: validation must fail before any HTTP call.
			_, err := tc.fn(newCtxWithInput(t, tc.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestParseJSONArray_RejectsNonArrayAndNamesTheField(t *testing.T) {
	_, err := parseJSONArray(`{"column_id":"Col1"}`, "cells")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cells must be a JSON array")

	out, err := parseJSONArray(`[{"column_id":"Col1"}]`, "cells")
	require.NoError(t, err)
	assert.Len(t, out, 1)
}

func TestCreateList_MapsCopyFromToSlackArgNames(t *testing.T) {
	// The input names are wick-side ergonomics; Slack expects
	// copy_from_list_id / include_copied_list_records.
	srv, rec := newRecordingSlack(t, `{"ok":true,"list_id":"F9"}`)
	withBaseURL(t, srv.URL)

	_, err := createList(newCtxWithInput(t, map[string]string{
		"name": "Sprint Board", "copy_from": "F1234567", "copy_records": "true", "todo_mode": "true",
	}))
	require.NoError(t, err)

	assert.Equal(t, "/slackLists.create", rec.path)
	assert.Equal(t, "F1234567", rec.body["copy_from_list_id"])
	assert.Equal(t, true, rec.body["include_copied_list_records"])
	assert.Equal(t, true, rec.body["todo_mode"])
}

func TestCreateListItem_MapsDuplicateFromToSlackArgName(t *testing.T) {
	srv, rec := newRecordingSlack(t, `{"ok":true,"item":{"id":"Rec1"}}`)
	withBaseURL(t, srv.URL)

	_, err := createListItem(newCtxWithInput(t, map[string]string{
		"list_id": "F1", "duplicate_from": "Rec9",
	}))
	require.NoError(t, err)
	assert.Equal(t, "Rec9", rec.body["duplicated_item_id"])
}

func TestGetListItem_SendsItemIDAsID(t *testing.T) {
	// Slack names the row argument `id`, not `item_id`.
	srv, rec := newRecordingSlack(t, `{"ok":true,"record":{"id":"Rec1"}}`)
	withBaseURL(t, srv.URL)

	_, err := getListItem(newCtxWithInput(t, map[string]string{"list_id": "F1", "item_id": "Rec1"}))
	require.NoError(t, err)
	assert.Equal(t, "Rec1", rec.body["id"])
	assert.NotContains(t, rec.body, "item_id")
}

func TestListOpsHaveScopeRules(t *testing.T) {
	// Every List op must be covered by the healthcheck, and list_lists in
	// particular needs files:read because it calls files.list.
	assert.Equal(t, [][]string{{"files:read"}}, opScopes["list_lists"])
	for _, op := range []string{"get_list", "list_list_items", "get_list_item"} {
		assert.Equal(t, [][]string{{"lists:read"}}, opScopes[op], op)
	}
	for _, op := range []string{"create_list", "create_list_item", "update_list_item", "delete_list_item"} {
		assert.Equal(t, [][]string{{"lists:write"}}, opScopes[op], op)
	}
}

func TestCustomAPICallHasNoScopeRule(t *testing.T) {
	// Its scopes depend on the method the caller names, so a static rule
	// would wrongly system-disable the op.
	_, ok := opScopes["custom_api_call"]
	assert.False(t, ok)
}

// ── Custom API: method validation ────────────────────────────────────

func TestNormalizeAPIMethod_AcceptsBareAndSlashPrefixed(t *testing.T) {
	for _, in := range []string{"conversations.members", "/conversations.members", "  emoji.list  "} {
		out, err := normalizeAPIMethod(in)
		require.NoErrorf(t, err, "input %q", in)
		assert.NotEmpty(t, out)
	}
	out, err := normalizeAPIMethod("/conversations.members")
	require.NoError(t, err)
	assert.Equal(t, "conversations.members", out)
}

func TestNormalizeAPIMethod_RejectsURLsAndTraversal(t *testing.T) {
	// The method is concatenated onto the base URL, so anything that could
	// steer the request elsewhere has to be refused.
	bad := []string{
		"", "   ",
		"https://evil.com/steal",
		"conversations.members?token=x",
		"../../admin.users.list",
		"foo/bar",
		"chat.postMessage#frag",
		"chat postMessage",
	}
	for _, in := range bad {
		_, err := normalizeAPIMethod(in)
		assert.Errorf(t, err, "expected %q to be rejected", in)
	}
}

// ── Custom API: allowlist gating ─────────────────────────────────────

func TestCheckMethodAllowed_DefaultModeIsAllowlist(t *testing.T) {
	// An instance that has never been configured must not behave as if
	// every method were permitted.
	c := newCustomCtx(t, map[string]string{}, map[string]string{})
	err := checkMethodAllowed(c, "chat.postMessage")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allowlist is empty")
}

func TestCheckMethodAllowed_ModeAllPermitsAnything(t *testing.T) {
	c := newCustomCtx(t, map[string]string{"custom_api_mode": "all"}, map[string]string{})
	require.NoError(t, checkMethodAllowed(c, "admin.users.session.reset"))
}

func TestCheckMethodAllowed_ExactAndWildcardEntries(t *testing.T) {
	c := newCustomCtx(t, map[string]string{
		"custom_api_mode":      "allowlist",
		"custom_api_allowlist": `[{"method":"emoji.list"},{"method":"admin.*"}]`,
	}, map[string]string{})

	require.NoError(t, checkMethodAllowed(c, "emoji.list"))
	require.NoError(t, checkMethodAllowed(c, "EMOJI.LIST")) // case-insensitive
	require.NoError(t, checkMethodAllowed(c, "admin.users.list"))

	err := checkMethodAllowed(c, "chat.postMessage")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in this connector's Custom API allowlist")
	assert.Contains(t, err.Error(), "emoji.list") // reason names what IS allowed
}

func TestCheckMethodAllowed_PrefixEntryDoesNotMatchUnrelatedMethod(t *testing.T) {
	c := newCustomCtx(t, map[string]string{
		"custom_api_mode":      "allowlist",
		"custom_api_allowlist": `[{"method":"conversations.*"}]`,
	}, map[string]string{})
	require.NoError(t, checkMethodAllowed(c, "conversations.members"))
	require.Error(t, checkMethodAllowed(c, "chat.conversations.members"))
}

func TestCheckMethodAllowed_UnknownModeIsAnError(t *testing.T) {
	c := newCustomCtx(t, map[string]string{"custom_api_mode": "everything"}, map[string]string{})
	err := checkMethodAllowed(c, "emoji.list")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown custom_api_mode")
}

func TestParseAllowlist_AcceptsKvlistJSONAndPlainText(t *testing.T) {
	assert.Equal(t, []string{"emoji.list", "pins.add"},
		parseAllowlist(`[{"method":"emoji.list"},{"method":"pins.add"}]`))
	// Hand-edited values stay usable when set outside the admin UI.
	assert.Equal(t, []string{"emoji.list", "pins.add"}, parseAllowlist("emoji.list, pins.add"))
	assert.Equal(t, []string{"emoji.list", "pins.add"}, parseAllowlist("emoji.list\npins.add"))
	assert.Nil(t, parseAllowlist("   "))
}

// ── Custom API: params ───────────────────────────────────────────────

func TestParseAPIParams_RejectsTokenKey(t *testing.T) {
	// The connector's own credential must stay the only source of auth.
	for _, raw := range []string{`{"token":"xoxb-evil"}`, `{"TOKEN":"xoxb-evil"}`} {
		_, err := parseAPIParams(raw)
		require.Errorf(t, err, "input %q", raw)
		assert.Contains(t, err.Error(), "must not contain a token")
	}
}

func TestParseAPIParams_RejectsNonObject(t *testing.T) {
	for _, raw := range []string{`["channel"]`, `"channel"`, `not json`} {
		_, err := parseAPIParams(raw)
		assert.Errorf(t, err, "expected %q to be rejected", raw)
	}
}

func TestParseAPIParams_EmptyIsAnEmptyObject(t *testing.T) {
	out, err := parseAPIParams("  ")
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestResolveHTTPVerb(t *testing.T) {
	// Explicit choice always wins over the auto heuristic.
	assert.Equal(t, http.MethodGet, resolveHTTPVerb("get", "chat.postMessage"))
	assert.Equal(t, http.MethodPost, resolveHTTPVerb("post", "conversations.list"))

	// auto: read-shaped suffixes go GET, everything else POST.
	for _, m := range []string{"conversations.list", "users.info", "conversations.history", "conversations.replies", "conversations.members"} {
		assert.Equalf(t, http.MethodGet, resolveHTTPVerb("auto", m), "method %q", m)
	}
	for _, m := range []string{"chat.postMessage", "pins.add", "slackLists.items.update"} {
		assert.Equalf(t, http.MethodPost, resolveHTTPVerb("auto", m), "method %q", m)
	}
	// Unset input falls back to the same auto behaviour.
	assert.Equal(t, http.MethodGet, resolveHTTPVerb("", "emoji.list"))
}

func TestStringifyParams_FlattensScalarsAndEncodesNested(t *testing.T) {
	out := stringifyParams(map[string]any{
		"channel": "C12345",
		"limit":   float64(50),
		"full":    true,
		"nested":  map[string]any{"a": float64(1)},
		"skipped": nil,
	})
	assert.Equal(t, "C12345", out["channel"])
	assert.Equal(t, "50", out["limit"]) // not "50.000000"
	assert.Equal(t, "true", out["full"])
	assert.JSONEq(t, `{"a":1}`, out["nested"])
	assert.NotContains(t, out, "skipped")
}

// ── Custom API: end-to-end through the handler ───────────────────────

func TestCustomAPICall_GETSendsParamsAsQuery(t *testing.T) {
	srv, rec := newRecordingSlack(t, `{"ok":true,"members":["U1"]}`)
	withBaseURL(t, srv.URL)

	out, err := customAPICall(newCustomCtx(t,
		map[string]string{"custom_api_mode": "all"},
		map[string]string{"method": "conversations.members", "params": `{"channel":"C12345","limit":100}`},
	))
	require.NoError(t, err)

	assert.Equal(t, "/conversations.members", rec.path)
	assert.Equal(t, http.MethodGet, rec.verb)
	assert.Equal(t, "C12345", rec.query.Get("channel"))
	assert.Equal(t, "100", rec.query.Get("limit"))

	m := out.(map[string]any)
	assert.Equal(t, "conversations.members", m["method"])
	assert.Equal(t, http.MethodGet, m["http"])
	assert.Contains(t, m, "response")
}

func TestCustomAPICall_POSTSendsParamsAsBody(t *testing.T) {
	srv, rec := newRecordingSlack(t, `{"ok":true}`)
	withBaseURL(t, srv.URL)

	_, err := customAPICall(newCustomCtx(t,
		map[string]string{"custom_api_mode": "all"},
		map[string]string{"method": "pins.add", "params": `{"channel":"C12345","timestamp":"1700000000.000100"}`},
	))
	require.NoError(t, err)

	assert.Equal(t, "/pins.add", rec.path)
	assert.Equal(t, http.MethodPost, rec.verb)
	assert.Equal(t, "C12345", rec.body["channel"])
	assert.Equal(t, "1700000000.000100", rec.body["timestamp"])
}

func TestCustomAPICall_BlockedMethodNeverReachesSlack(t *testing.T) {
	// The gate must run before any outbound request.
	srv, rec := newRecordingSlack(t, `{"ok":true}`)
	withBaseURL(t, srv.URL)

	_, err := customAPICall(newCustomCtx(t,
		map[string]string{"custom_api_mode": "allowlist", "custom_api_allowlist": `[{"method":"emoji.list"}]`},
		map[string]string{"method": "chat.delete", "params": `{"channel":"C1","ts":"1"}`},
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in this connector's Custom API allowlist")
	assert.Empty(t, rec.path, "blocked call must not hit Slack")
}

func TestCustomAPICall_SurfacesSlackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)

	_, err := customAPICall(newCustomCtx(t,
		map[string]string{"custom_api_mode": "all"},
		map[string]string{"method": "emoji.list"},
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing_scope")
}

// ── Registration wiring ──────────────────────────────────────────────

func TestOperations_ExposesListsAndCustomCategories(t *testing.T) {
	byTitle := map[string][]string{}
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			byTitle[cat.Title] = append(byTitle[cat.Title], op.Key)
		}
	}
	assert.ElementsMatch(t,
		[]string{"list_lists", "get_list", "list_list_items", "get_list_item",
			"create_list", "create_list_item", "update_list_item", "delete_list_item"},
		byTitle["Lists"])
	assert.Equal(t, []string{"custom_api_call"}, byTitle["Custom"])
}

func TestWriteOpsAreDestructiveSoTheyDefaultOff(t *testing.T) {
	want := map[string]bool{
		"list_lists": false, "get_list": false, "list_list_items": false, "get_list_item": false,
		"create_list": true, "create_list_item": true, "update_list_item": true, "delete_list_item": true,
		// The escape hatch must start disabled on every new instance.
		"custom_api_call": true,
	}
	got := map[string]bool{}
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if _, tracked := want[op.Key]; tracked {
				got[op.Key] = op.Destructive
			}
		}
	}
	assert.Equal(t, want, got)
}
