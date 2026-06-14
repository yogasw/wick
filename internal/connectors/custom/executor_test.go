package custom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

type recordedReq struct {
	method string
	path   string
	query  string
	header http.Header
	body   string
}

func TestExecuteHTTPRendersRecipe(t *testing.T) {
	var got recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = recordedReq{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			header: r.Header.Clone(),
			body:   string(b),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"count":2}`))
	}))
	defer srv.Close()

	recipe := OpRequest{
		Method:      "POST",
		URLTemplate: "{{.cfg.base_url}}/v1/items/{{.in.item_id}}?q={{urlquery .in.q}}",
		Headers: map[string]string{
			"Authorization": "Bearer {{.cfg.token}}",
			"X-Static":      "yes",
		},
		BodyTemplate: `{"name": "{{js .in.name}}"}`,
		ContentType:  "application/json",
	}
	cfg := map[string]string{"base_url": srv.URL, "token": "tok-1"}
	in := map[string]string{"item_id": "42", "q": "a b", "name": "Ann"}
	c := connector.NewCtx(context.Background(), "inst-1", cfg, in, srv.Client(), nil, nil)

	res, err := executeHTTP(c, recipe, []string{"base_url", "token"}, []string{"item_id", "q", "name"})
	if err != nil {
		t.Fatalf("executeHTTP: %v", err)
	}

	if got.method != "POST" {
		t.Errorf("upstream method = %q", got.method)
	}
	if got.path != "/v1/items/42" {
		t.Errorf("upstream path = %q", got.path)
	}
	if got.query != "q=a+b" {
		t.Errorf("upstream query = %q", got.query)
	}
	if h := got.header.Get("Authorization"); h != "Bearer tok-1" {
		t.Errorf("Authorization = %q", h)
	}
	if h := got.header.Get("X-Static"); h != "yes" {
		t.Errorf("X-Static = %q", h)
	}
	if h := got.header.Get("Content-Type"); h != "application/json" {
		t.Errorf("Content-Type = %q", h)
	}
	if got.body != `{"name": "Ann"}` {
		t.Errorf("upstream body = %q", got.body)
	}

	want := map[string]any{"ok": true, "count": float64(2)}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %#v, want %#v", res, want)
	}
}

func TestExecuteHTTPNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal boom"))
	}))
	defer srv.Close()

	recipe := OpRequest{Method: "GET", URLTemplate: "{{.cfg.base_url}}/x"}
	c := connector.NewCtx(context.Background(), "i", map[string]string{"base_url": srv.URL}, nil, srv.Client(), nil, nil)

	_, err := executeHTTP(c, recipe, []string{"base_url"}, nil)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "upstream HTTP 500") {
		t.Errorf("error missing status: %v", err)
	}
	if !strings.Contains(err.Error(), "internal boom") {
		t.Errorf("error missing body snippet: %v", err)
	}
}

func TestExecuteHTTPNonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	recipe := OpRequest{Method: "GET", URLTemplate: "{{.cfg.base_url}}/ping"}
	c := connector.NewCtx(context.Background(), "i", map[string]string{"base_url": srv.URL}, nil, srv.Client(), nil, nil)

	res, err := executeHTTP(c, recipe, []string{"base_url"}, nil)
	if err != nil {
		t.Fatalf("executeHTTP: %v", err)
	}
	if res != "pong" {
		t.Errorf("result = %#v, want raw string \"pong\"", res)
	}
}

func TestExecuteHTTPNonHTTPURL(t *testing.T) {
	recipe := OpRequest{Method: "GET", URLTemplate: "{{.cfg.base_url}}/etc"}
	c := connector.NewCtx(context.Background(), "i", map[string]string{"base_url": "file:///tmp"}, nil, nil, nil, nil)

	_, err := executeHTTP(c, recipe, []string{"base_url"}, nil)
	if err == nil || !strings.Contains(err.Error(), "is not http(s)") {
		t.Fatalf("err = %v, want non-http(s) rejection", err)
	}
}

func TestExecuteHTTPTemplateErrorSurfaces(t *testing.T) {
	recipe := OpRequest{
		Method:       "POST",
		URLTemplate:  "{{.cfg.base_url}}/x",
		BodyTemplate: "{{.in.missing}}",
	}
	c := connector.NewCtx(context.Background(), "i", map[string]string{"base_url": "https://example.com"}, map[string]string{"name": "x"}, nil, nil, nil)

	_, err := executeHTTP(c, recipe, []string{"base_url"}, []string{"name"})
	if err == nil || !strings.Contains(err.Error(), "body template") {
		t.Fatalf("err = %v, want body template error", err)
	}
}

func TestCoerceArgs(t *testing.T) {
	fields := []DefField{
		{Key: "n", Widget: "number"},
		{Key: "bad_n", Widget: "number"},
		{Key: "cb", Widget: "checkbox"},
		{Key: "cb_off", Widget: "checkbox"},
		{Key: "flag", Widget: "bool"},
		{Key: "obj", Widget: "textarea"},
		{Key: "arr", Widget: "textarea"},
		{Key: "txt", Widget: "textarea"},
		{Key: "plain", Widget: "text"},
		{Key: "absent", Widget: "text"},
	}
	in := map[string]string{
		"n":      "3.5",
		"bad_n":  "abc",
		"cb":     "yes",
		"cb_off": "false",
		"flag":   "1",
		"obj":    `{"a":1}`,
		"arr":    `[1,2]`,
		"txt":    "hello",
		"plain":  "v",
	}
	c := connector.NewCtx(context.Background(), "i", nil, in, nil, nil, nil)

	out := coerceArgs(fields, c)

	if v, ok := out["n"].(float64); !ok || v != 3.5 {
		t.Errorf("n = %#v, want float64 3.5", out["n"])
	}
	if v, ok := out["bad_n"].(string); !ok || v != "abc" {
		t.Errorf("bad_n = %#v, want raw string fallback", out["bad_n"])
	}
	if v, ok := out["cb"].(bool); !ok || !v {
		t.Errorf("cb = %#v, want true", out["cb"])
	}
	if v, ok := out["cb_off"].(bool); !ok || v {
		t.Errorf("cb_off = %#v, want false", out["cb_off"])
	}
	if v, ok := out["flag"].(bool); !ok || !v {
		t.Errorf("flag = %#v, want true", out["flag"])
	}
	if !reflect.DeepEqual(out["obj"], map[string]any{"a": float64(1)}) {
		t.Errorf("obj = %#v, want decoded map", out["obj"])
	}
	if !reflect.DeepEqual(out["arr"], []any{float64(1), float64(2)}) {
		t.Errorf("arr = %#v, want decoded array", out["arr"])
	}
	if out["txt"] != "hello" {
		t.Errorf("txt = %#v, want passthrough string", out["txt"])
	}
	if out["plain"] != "v" {
		t.Errorf("plain = %#v", out["plain"])
	}
	if _, present := out["absent"]; present {
		t.Error("empty input must be omitted from args")
	}
}

// TestToolsToOps covers the live-catalog → operation mapping: exclusion
// filter, slug-collision dedup, destructive name guess, and the default
// description for tools that ship none.
func TestToolsToOps(t *testing.T) {
	tools := []MCPTool{
		{Name: "list_users", Description: "List all users."},
		{Name: "delete_user", Description: "Remove a user."},
		{Name: "noisy_tool", Description: "Excluded by admin."},
		{Name: "list-users"}, // slugs to list_users — dropped as a collision
	}
	ops := toolsToOps("srv-1", tools, map[string]bool{"noisy_tool": true})

	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2 (excluded + collision dropped): %#v", len(ops), ops)
	}
	if ops[0].Key != "list_users" || ops[0].Destructive {
		t.Errorf("ops[0] = %+v, want non-destructive list_users", ops[0])
	}
	if ops[1].Key != "delete_user" || !ops[1].Destructive {
		t.Errorf("ops[1] = %+v, want destructive delete_user", ops[1])
	}
	for _, op := range ops {
		if op.MCPSource == nil || op.MCPSource.ServerID != "srv-1" {
			t.Errorf("op %s: MCPSource = %+v, want server srv-1", op.Key, op.MCPSource)
		}
		if op.Description == "" {
			t.Errorf("op %s: empty description must default", op.Key)
		}
	}
}

func TestParseExcluded(t *testing.T) {
	if got := parseExcluded(""); len(got) != 0 {
		t.Errorf("empty column: got %#v", got)
	}
	if got := parseExcluded("not json"); len(got) != 0 {
		t.Errorf("bad json must yield empty set, got %#v", got)
	}
	got := parseExcluded(`["a","b"]`)
	if !got["a"] || !got["b"] || got["c"] {
		t.Errorf("got %#v, want {a,b}", got)
	}
}

// TestCoerceArgsUsesOriginalNames: outbound MCP arguments must carry
// the server's original property names (Label), not wick's slugged
// keys — libraryId slugs to libraryid for the input map, and sending
// the slug back fails the server-side schema validation.
func TestCoerceArgsUsesOriginalNames(t *testing.T) {
	fields := []DefField{
		{Key: "libraryid", Label: "libraryId", Widget: "text"},
		{Key: "no_label", Widget: "text"},
	}
	in := map[string]string{"libraryid": "vercel/next.js", "no_label": "x"}
	c := connector.NewCtx(context.Background(), "i", nil, in, nil, nil, nil)

	out := coerceArgs(fields, c)

	if out["libraryId"] != "vercel/next.js" {
		t.Errorf("expected original name libraryId, got %#v", out)
	}
	if _, leaked := out["libraryid"]; leaked {
		t.Error("slugged key must not be sent upstream")
	}
	if out["no_label"] != "x" {
		t.Errorf("label-less field must fall back to key, got %#v", out)
	}
}

func TestHealthCheckForVerdict(t *testing.T) {
	opKeys := []string{"ping", "list"}

	cases := []struct {
		name       string
		probe      connector.ExecuteFunc
		expect     string
		wantOK     bool
		wantReason string // substring; empty = no reason expected
	}{
		{
			name:   "executes ok no expect",
			probe:  func(*connector.Ctx) (any, error) { return map[string]any{"ok": true}, nil },
			wantOK: true,
		},
		{
			name:       "execute error fails all",
			probe:      func(*connector.Ctx) (any, error) { return nil, errString("upstream HTTP 401: bad key") },
			wantOK:     false,
			wantReason: "401",
		},
		{
			name:   "expect substring present in json",
			probe:  func(*connector.Ctx) (any, error) { return map[string]any{"ok": true}, nil },
			expect: `"ok":true`,
			wantOK: true,
		},
		{
			name:       "expect substring missing",
			probe:      func(*connector.Ctx) (any, error) { return map[string]any{"ok": false}, nil },
			expect:     `"ok":true`,
			wantOK:     false,
			wantReason: "did not contain",
		},
		{
			name:   "expect matches raw string body",
			probe:  func(*connector.Ctx) (any, error) { return "pong", nil },
			expect: "pong",
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hc := healthCheckFor(tc.probe, opKeys, tc.expect)
			report, err := hc(connector.NewCtx(context.Background(), "i", nil, nil, nil, nil, nil))
			if err != nil {
				t.Fatalf("health check returned error: %v", err)
			}
			if len(report) != len(opKeys) {
				t.Fatalf("report has %d entries, want %d (one per op)", len(report), len(opKeys))
			}
			for _, h := range report {
				if h.OK != tc.wantOK {
					t.Errorf("op %q OK = %v, want %v", h.Key, h.OK, tc.wantOK)
				}
				if tc.wantReason != "" && !strings.Contains(h.Reason, tc.wantReason) {
					t.Errorf("op %q reason = %q, want substring %q", h.Key, h.Reason, tc.wantReason)
				}
			}
		})
	}
}

// errString is a tiny error helper so the test does not pull in fmt.
type errString string

func (e errString) Error() string { return string(e) }
