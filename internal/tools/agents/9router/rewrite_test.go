package router9

import "testing"

func TestPrefixAbsolute(t *testing.T) {
	cases := map[string]string{"/dashboard": "/9router/dashboard", "/login": "/9router/login", "/9router/x": "/9router/x", "//host/x": "//host/x", "http://x": "http://x", "": ""}
	for in, want := range cases {
		if got := prefixAbsolute(in); got != want {
			t.Errorf("prefixAbsolute(%q)=%q want %q", in, got, want)
		}
	}
}
func TestRewriteHTML(t *testing.T) {
	in := `<html><head><meta></head><body><a href="/login">x</a><script src="/_next/a.js"></script><img src="/9router/already.png"></body></html>`
	got := rewriteHTML(in)
	for _, want := range []string{`<base href="/9router/">`, `href="/9router/login"`, `src="/9router/already.png"`} {
		if !contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// /_next/ is intentionally NOT rewritten — it is served at the wick root
	// by Router9NextAssetProxy, so it must stay root-absolute here.
	if !contains(got, `src="/_next/a.js"`) {
		t.Errorf("/_next/ should be left untouched, got:\n%s", got)
	}
	// no double prefix
	if contains(got, "/9router/9router/") {
		t.Errorf("double prefix:\n%s", got)
	}
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
