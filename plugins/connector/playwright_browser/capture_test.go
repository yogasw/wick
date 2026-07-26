package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestCaptureFilter(t *testing.T) {
	// No pattern → keep XHR/fetch-ish, skip static assets.
	cp := newCapture("", false)
	if cp.urlRE != nil {
		t.Error("empty pattern should leave urlRE nil")
	}

	// Pattern as plain substring compiles and matches.
	cp = newCapture("/api/graphql", false)
	if cp.urlRE == nil || !cp.urlRE.MatchString("https://x.com/api/graphql?a=1") {
		t.Error("substring pattern should match")
	}
	if cp.urlRE.MatchString("https://x.com/other") {
		t.Error("pattern should not match unrelated url")
	}

	// Regex pattern works too.
	cp = newCapture(`/api/(graphql|rest)`, false)
	if !cp.urlRE.MatchString("https://x.com/api/rest") {
		t.Error("regex alternation should match")
	}

	// An invalid regex falls back to a literal match (QuoteMeta), not a panic.
	cp = newCapture("a[b", false)
	if cp.urlRE == nil || !cp.urlRE.MatchString("x a[b y") {
		t.Error("invalid regex should fall back to literal match")
	}
}

func TestAssetExtRE(t *testing.T) {
	assets := []string{
		"https://x.com/a.png", "https://x.com/s.css?v=2", "https://x.com/f.woff2",
		"https://x.com/app.js", "https://x.com/img.JPEG", "https://x.com/m.mp4",
	}
	for _, u := range assets {
		if !assetExtRE.MatchString(u) {
			t.Errorf("expected %q to be detected as a static asset", u)
		}
	}
	nonAssets := []string{
		"https://x.com/api/graphql", "https://x.com/users/42", "https://x.com/j",
		"https://x.com/data.json",
	}
	for _, u := range nonAssets {
		if assetExtRE.MatchString(u) {
			t.Errorf("expected %q NOT to be a static asset", u)
		}
	}
}

func TestCaptureSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "captured.json")

	want := []CapturedRequest{
		{Method: "POST", URL: "https://x.com/api", Headers: map[string]string{"x-a": "1"}, Cookies: "s=abc", Body: `{"q":1}`, Status: 200},
		{Method: "GET", URL: "https://x.com/me", Headers: map[string]string{}, Status: 401},
	}
	if err := saveCapture(path, want); err != nil {
		t.Fatalf("saveCapture: %v", err)
	}
	got, err := loadCapture(path)
	if err != nil {
		t.Fatalf("loadCapture: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}

	// Missing file → empty, not an error (callers may poll before recording).
	empty, err := loadCapture(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("missing file should yield empty slice, got %d", len(empty))
	}
}

// req is a tiny helper to build a CapturedRequest for addRecord tests.
func req(method, url, body string) CapturedRequest {
	return CapturedRequest{Method: method, URL: url, Body: body, Status: 200}
}

// TestCaptureDedup: identical (method+URL+body) requests collapse to one; a
// different body OR method OR url is a distinct entry.
func TestCaptureDedup(t *testing.T) {
	cp := newCapture("", true) // include assets so the filter never interferes
	cp.addRecord(req("GET", "https://a.com/api/poll", ""))
	cp.addRecord(req("GET", "https://a.com/api/poll", "")) // exact dup → dropped
	cp.addRecord(req("GET", "https://a.com/api/poll", "")) // dup again → dropped
	if got := cp.snapshot(); len(got) != 1 {
		t.Fatalf("exact duplicates should collapse to 1, got %d", len(got))
	}

	// Same URL+method, different body → kept (paginated POSTs).
	cp.addRecord(req("POST", "https://a.com/api/list", `{"page":1}`))
	cp.addRecord(req("POST", "https://a.com/api/list", `{"page":2}`))
	cp.addRecord(req("POST", "https://a.com/api/list", `{"page":1}`)) // dup of first
	list := 0
	for _, r := range cp.snapshot() {
		if r.URL == "https://a.com/api/list" {
			list++
		}
	}
	if list != 2 {
		t.Fatalf("different-body POSTs should be kept separately (want 2), got %d", list)
	}
}

// TestCaptureCrossDomain: with no url pattern, requests to DIFFERENT domains are
// all recorded (cross-domain is kept by design). Re-hitting the same domain
// endpoint dedups, but distinct domains never collide even for the same path.
func TestCaptureCrossDomain(t *testing.T) {
	cp := newCapture("", true)
	cp.addRecord(req("GET", "https://a.com/api/x", ""))
	cp.addRecord(req("GET", "https://b.com/api/x", "")) // same path, other domain
	cp.addRecord(req("GET", "https://c.com/api/x", ""))
	cp.addRecord(req("GET", "https://a.com/api/x", "")) // dup of #1 → dropped
	got := cp.snapshot()
	if len(got) != 3 {
		t.Fatalf("three distinct domains should record 3 (dup dropped), got %d", len(got))
	}
	seenDomain := map[string]bool{}
	for _, r := range got {
		seenDomain[r.URL] = true
	}
	for _, want := range []string{"https://a.com/api/x", "https://b.com/api/x", "https://c.com/api/x"} {
		if !seenDomain[want] {
			t.Errorf("missing cross-domain request %q", want)
		}
	}
}

// TestCaptureURLPatternFilter: with a pattern set, only matching URLs are kept —
// regardless of domain.
func TestCaptureURLPatternFilter(t *testing.T) {
	cp := newCapture("/api/", true)
	cp.addRecord(req("GET", "https://a.com/api/users", ""))  // matches
	cp.addRecord(req("GET", "https://b.com/api/orders", "")) // matches (other domain)
	cp.addRecord(req("GET", "https://a.com/static/logo", "")) // no match → dropped
	if got := cp.snapshot(); len(got) != 2 {
		t.Fatalf("pattern should keep 2 (both /api/, any domain), got %d", len(got))
	}
}

// TestCaptureAssetFilter: static assets are dropped unless includeAssets is set.
func TestCaptureAssetFilter(t *testing.T) {
	skip := newCapture("", false)
	skip.addRecord(req("GET", "https://a.com/app.js", ""))
	skip.addRecord(req("GET", "https://a.com/api/data", ""))
	if got := skip.snapshot(); len(got) != 1 || got[0].URL != "https://a.com/api/data" {
		t.Fatalf("assets should be skipped by default, got %+v", got)
	}

	keep := newCapture("", true)
	keep.addRecord(req("GET", "https://a.com/app.js", ""))
	keep.addRecord(req("GET", "https://a.com/api/data", ""))
	if got := keep.snapshot(); len(got) != 2 {
		t.Fatalf("includeAssets=true should keep the asset too, got %d", len(got))
	}
}

// TestCaptureMaxItems: recording stops at maxItems; the cap doesn't corrupt the
// dedup set or panic.
func TestCaptureMaxItems(t *testing.T) {
	cp := newCapture("", true)
	cp.maxItems = 3
	for i := 0; i < 10; i++ {
		cp.addRecord(req("GET", "https://a.com/api/"+string(rune('a'+i)), ""))
	}
	if got := cp.snapshot(); len(got) != 3 {
		t.Fatalf("maxItems=3 should cap at 3, got %d", len(got))
	}
}

// TestGetRequestOpRegistered guards the read-only op is on the module.
func TestGetRequestOpRegistered(t *testing.T) {
	var found bool
	for _, op := range Module().AllOps() {
		if op.Key == "get_request" {
			found = true
			if op.Destructive {
				t.Error("get_request should be read-only, not destructive")
			}
		}
	}
	if !found {
		t.Fatal("get_request op not registered")
	}
}
