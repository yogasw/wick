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
