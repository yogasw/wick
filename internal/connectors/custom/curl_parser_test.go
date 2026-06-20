package custom

import (
	"encoding/base64"
	"strings"
	"testing"
)

func findCfg(t *testing.T, d *Draft, key string) DefField {
	t.Helper()
	for _, c := range d.Configs {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("config %q not found in %+v", key, d.Configs)
	return DefField{}
}

func findInput(t *testing.T, op DefOp, key string) DefField {
	t.Helper()
	for _, f := range op.Inputs {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("input %q not found in %+v", key, op.Inputs)
	return DefField{}
}

func TestParseCurlAndExtract(t *testing.T) {
	cases := []struct {
		name     string
		paste    string
		parseErr string // expected substring of the ParseCurl error
		check    func(t *testing.T, p *ParsedRequest, d *Draft)
	}{
		{
			name:  "GET with query string and bearer header",
			paste: `curl 'https://api.example.com/v1/items?limit=10&q=test' -H 'Authorization: Bearer abc123'`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				op := d.Ops[0].Ops[0]
				if p.Method != "GET" || op.Request.Method != "GET" {
					t.Errorf("method = %q / %q, want GET", p.Method, op.Request.Method)
				}
				want := "{{.cfg.base_url}}/v1/items?limit={{urlquery .in.limit}}&q={{urlquery .in.q}}"
				if op.Request.URLTemplate != want {
					t.Errorf("url_template = %q, want %q", op.Request.URLTemplate, want)
				}
				base := findCfg(t, d, "base_url")
				if base.Default != "https://api.example.com" {
					t.Errorf("base_url default = %q", base.Default)
				}
				auth := findCfg(t, d, "auth_value")
				if !auth.Secret || auth.Widget != "secret" || auth.Default != "abc123" {
					t.Errorf("auth_value config = %+v, want secret widget with token default", auth)
				}
				if got := op.Request.Headers["Authorization"]; got != "Bearer {{.cfg.auth_value}}" {
					t.Errorf("Authorization header template = %q", got)
				}
				if lim := findInput(t, op, "limit"); lim.Default != "10" {
					t.Errorf("limit input default = %q", lim.Default)
				}
				if d.Key != "example_com" {
					t.Errorf("draft key = %q, want example_com (api. prefix trimmed)", d.Key)
				}
				if op.Key != "get_items" {
					t.Errorf("op key = %q, want get_items", op.Key)
				}
			},
		},
		{
			name:  "POST JSON body with nested object",
			paste: `curl -X POST https://example.com/users -H 'Content-Type: application/json' -d '{"name":"Bob","age":30,"active":true,"meta":{"tier":"gold"}}'`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				op := d.Ops[0].Ops[0]
				if op.Request.Method != "POST" {
					t.Errorf("method = %q", op.Request.Method)
				}
				if op.Request.ContentType != "application/json" {
					t.Errorf("content_type = %q", op.Request.ContentType)
				}
				// Keys are emitted sorted: active, age, meta, name.
				want := `{ "active": {{.in.active}}, "age": {{.in.age}}, "meta": {{.in.meta}}, "name": "{{js .in.name}}" }`
				if op.Request.BodyTemplate != want {
					t.Errorf("body_template = %q\nwant %q", op.Request.BodyTemplate, want)
				}
				if f := findInput(t, op, "active"); f.Widget != "checkbox" || f.Default != "true" {
					t.Errorf("active input = %+v, want checkbox/true", f)
				}
				if f := findInput(t, op, "age"); f.Widget != "number" || f.Default != "30" {
					t.Errorf("age input = %+v, want number/30", f)
				}
				if f := findInput(t, op, "meta"); f.Widget != "textarea" || f.Default != `{"tier":"gold"}` {
					t.Errorf("meta input = %+v, want textarea raw JSON", f)
				}
				if f := findInput(t, op, "name"); f.Widget != "text" || f.Default != "Bob" {
					t.Errorf("name input = %+v, want text/Bob", f)
				}
			},
		},
		{
			name:  "POST form-encoded -d pairs",
			paste: `curl https://example.com/login -d 'username=bob&password=hunter2'`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				op := d.Ops[0].Ops[0]
				if op.Request.Method != "POST" {
					t.Errorf("method = %q, want implicit POST from -d", op.Request.Method)
				}
				if op.Request.ContentType != "application/x-www-form-urlencoded" {
					t.Errorf("content_type = %q", op.Request.ContentType)
				}
				want := "username={{urlquery .in.username}}&password={{urlquery .in.password}}"
				if op.Request.BodyTemplate != want {
					t.Errorf("body_template = %q", op.Request.BodyTemplate)
				}
				if f := findInput(t, op, "password"); !f.Secret {
					t.Errorf("password input not marked secret: %+v", f)
				}
				if f := findInput(t, op, "username"); f.Default != "bob" {
					t.Errorf("username default = %q", f.Default)
				}
			},
		},
		{
			name:  "data-urlencode pairs join with ampersand",
			paste: `curl https://example.com/search --data-urlencode 'q=hello world' --data-urlencode 'lang=en'`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				if p.Body != "q=hello world&lang=en" {
					t.Errorf("body = %q", p.Body)
				}
				op := d.Ops[0].Ops[0]
				if op.Request.Method != "POST" {
					t.Errorf("method = %q", op.Request.Method)
				}
				if f := findInput(t, op, "q"); f.Default != "hello world" {
					t.Errorf("q default = %q", f.Default)
				}
				if f := findInput(t, op, "lang"); f.Default != "en" {
					t.Errorf("lang default = %q", f.Default)
				}
				want := "q={{urlquery .in.q}}&lang={{urlquery .in.lang}}"
				if op.Request.BodyTemplate != want {
					t.Errorf("body_template = %q", op.Request.BodyTemplate)
				}
			},
		},
		{
			name:  "basic auth via -u",
			paste: `curl -u admin:s3cret https://example.com/status`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				wantB64 := base64.StdEncoding.EncodeToString([]byte("admin:s3cret"))
				cfg := findCfg(t, d, "auth_basic")
				if !cfg.Secret || cfg.Default != wantB64 {
					t.Errorf("auth_basic = %+v, want secret with default %q", cfg, wantB64)
				}
				op := d.Ops[0].Ops[0]
				if got := op.Request.Headers["Authorization"]; got != "Basic {{.cfg.auth_basic}}" {
					t.Errorf("Authorization template = %q", got)
				}
			},
		},
		{
			name:  "PUT with numeric path segment becomes path input",
			paste: `curl -X PUT 'https://example.com/api/users/42' -H 'Content-Type: application/json' -d '{"name":"Ann"}'`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				op := d.Ops[0].Ops[0]
				if op.Request.Method != "PUT" {
					t.Errorf("method = %q", op.Request.Method)
				}
				want := "{{.cfg.base_url}}/api/users/{{.in.user_id}}"
				if op.Request.URLTemplate != want {
					t.Errorf("url_template = %q, want %q", op.Request.URLTemplate, want)
				}
				if f := findInput(t, op, "user_id"); !f.Required {
					t.Errorf("user_id should be required: %+v", f)
				}
				findInput(t, op, "name") // body field still extracted
				if op.Destructive {
					t.Error("PUT must not be destructive")
				}
			},
		},
		{
			name:  "multiline bash backslash continuation",
			paste: "curl https://example.com/api/v2/ping \\\n  -H 'Accept: application/json' \\\n  -H 'X-Trace: 1'",
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				op := d.Ops[0].Ops[0]
				if op.Request.URLTemplate != "{{.cfg.base_url}}/api/v2/ping" {
					t.Errorf("url_template = %q", op.Request.URLTemplate)
				}
				if got := op.Request.Headers["Accept"]; got != "application/json" {
					t.Errorf("Accept = %q", got)
				}
				if got := op.Request.Headers["X-Trace"]; got != "1" {
					t.Errorf("X-Trace = %q", got)
				}
				if op.Key != "get_ping" {
					t.Errorf("op key = %q", op.Key)
				}
			},
		},
		{
			name:  "PowerShell backtick continuation",
			paste: "curl https://example.com/data `\n  -H \"Accept: application/json\"",
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				op := d.Ops[0].Ops[0]
				if p.Method != "GET" {
					t.Errorf("method = %q", p.Method)
				}
				if op.Request.URLTemplate != "{{.cfg.base_url}}/data" {
					t.Errorf("url_template = %q", op.Request.URLTemplate)
				}
				if got := op.Request.Headers["Accept"]; got != "application/json" {
					t.Errorf("Accept = %q", got)
				}
			},
		},
		{
			name:  "single vs double quotes with escapes",
			paste: `curl "https://example.com/notes" -X POST -H 'X-Mode: a b' -d "{\"note\":\"hi\"}"`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				if p.Body != `{"note":"hi"}` {
					t.Errorf("body = %q, escapes not unwrapped", p.Body)
				}
				op := d.Ops[0].Ops[0]
				if got := op.Request.Headers["X-Mode"]; got != "a b" {
					t.Errorf("X-Mode = %q, single-quoted space lost", got)
				}
				if f := findInput(t, op, "note"); f.Default != "hi" {
					t.Errorf("note default = %q", f.Default)
				}
			},
		},
		{
			name:  "long-form flags --request --url --header",
			paste: `curl --request GET --url https://example.com/v1/ping --header 'X-Custom: yes'`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				op := d.Ops[0].Ops[0]
				if op.Request.Method != "GET" {
					t.Errorf("method = %q", op.Request.Method)
				}
				if op.Request.URLTemplate != "{{.cfg.base_url}}/v1/ping" {
					t.Errorf("url_template = %q", op.Request.URLTemplate)
				}
				if got := op.Request.Headers["X-Custom"]; got != "yes" {
					t.Errorf("X-Custom = %q", got)
				}
			},
		},
		{
			name:  "unknown flags tolerated and arg-flags consumed",
			paste: `curl -s -k --compressed -o out.json -A 'agent/1.0' https://example.com/data`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				if p.URL != "https://example.com/data" {
					t.Errorf("URL = %q, flag argument mistaken for the endpoint", p.URL)
				}
				if p.Method != "GET" {
					t.Errorf("method = %q", p.Method)
				}
			},
		},
		{
			name: "DevTools copy-as-cURL realistic sample",
			paste: "curl 'https://api.github.com/repos/acme/widgets/issues?state=open&per_page=5' \\\n" +
				"  -H 'accept: application/vnd.github+json' \\\n" +
				"  -H 'authorization: Bearer ghp_secret123' \\\n" +
				"  -H 'x-github-api-version: 2022-11-28' \\\n" +
				"  --compressed",
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				if d.Key != "github_com" {
					t.Errorf("draft key = %q", d.Key)
				}
				op := d.Ops[0].Ops[0]
				want := "{{.cfg.base_url}}/repos/acme/widgets/issues?per_page={{urlquery .in.per_page}}&state={{urlquery .in.state}}"
				if op.Request.URLTemplate != want {
					t.Errorf("url_template = %q\nwant %q", op.Request.URLTemplate, want)
				}
				auth := findCfg(t, d, "auth_value")
				if !auth.Secret || auth.Default != "ghp_secret123" {
					t.Errorf("auth_value = %+v", auth)
				}
				if got := op.Request.Headers["authorization"]; got != "Bearer {{.cfg.auth_value}}" {
					t.Errorf("authorization template = %q", got)
				}
				if got := op.Request.Headers["x-github-api-version"]; got != "2022-11-28" {
					t.Errorf("literal header lost: %q", got)
				}
				if op.Key != "get_issues" {
					t.Errorf("op key = %q", op.Key)
				}
			},
		},
		{
			name:     "not a curl paste",
			paste:    `fetch("https://example.com/x").then(r => r.json())`,
			parseErr: "not a cURL command",
		},
		{
			name:     "missing URL",
			paste:    `curl -X POST -H 'Accept: application/json'`,
			parseErr: "no URL found",
		},
		{
			name:  "DELETE is destructive with id path input",
			paste: `curl -X DELETE https://example.com/items/99`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				op := d.Ops[0].Ops[0]
				if !op.Destructive {
					t.Error("DELETE op must be destructive")
				}
				if op.Request.URLTemplate != "{{.cfg.base_url}}/items/{{.in.item_id}}" {
					t.Errorf("url_template = %q", op.Request.URLTemplate)
				}
				findInput(t, op, "item_id")
			},
		},
		{
			name:  "schemeless URL with secret query param",
			paste: `curl 'example.com/q?api_key=zzz'`,
			check: func(t *testing.T, p *ParsedRequest, d *Draft) {
				if p.URL != "https://example.com/q?api_key=zzz" {
					t.Errorf("URL = %q, https not prefixed", p.URL)
				}
				op := d.Ops[0].Ops[0]
				if f := findInput(t, op, "api_key"); !f.Secret {
					t.Errorf("api_key input not marked secret: %+v", f)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParseCurl(tc.paste)
			if tc.parseErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.parseErr) {
					t.Fatalf("ParseCurl error = %v, want substring %q", err, tc.parseErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCurl: %v", err)
			}
			d, err := Extract(p)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if len(d.AllOps()) != 1 {
				t.Fatalf("ops = %d, want 1", len(d.AllOps()))
			}
			if d.Ops[0].Ops[0].Request == nil {
				t.Fatal("op.Request is nil")
			}
			tc.check(t, p, d)
		})
	}
}

func TestTokenizeCurlUnterminatedQuote(t *testing.T) {
	if _, err := ParseCurl(`curl 'https://example.com/x`); err == nil {
		t.Fatal("expected error for unterminated quote")
	}
}
