package plugin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleCatalog = `[
  {"key":"gmail","name":"Gmail","description":"Gmail connector","version":"1.2.0",
   "assets":{"linux/arm64":"https://example.com/gmail-1.2.0-linux-arm64.zip",
             "linux/amd64":"https://example.com/gmail-1.2.0-linux-amd64.zip"}},
  {"name":"slack","description":"Slack","version":"0.9.1",
   "assets":{"darwin/arm64":"https://example.com/slack-0.9.1-darwin-arm64.zip"}},
  {"description":"skip me, no key/name","version":"1.0.0","assets":{}}
]`

func TestParseCatalog(t *testing.T) {
	got, err := parseCatalog([]byte(sampleCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries (keyless/nameless skipped), got %d: %+v", len(got), got)
	}
	// Sorted by key → gmail first. gmail has explicit key+name.
	if got[0].Key != "gmail" || got[0].Name != "Gmail" || got[0].Version != "1.2.0" {
		t.Errorf("first = key=%s name=%s v%s, want gmail/Gmail/1.2.0", got[0].Key, got[0].Name, got[0].Version)
	}
	if got[0].AssetFor("linux/arm64") == "" {
		t.Error("gmail should have linux/arm64 asset")
	}
	if got[0].AssetFor("windows/amd64") != "" {
		t.Error("gmail should NOT have windows/amd64")
	}
	// slack entry only had "name" → Key backfilled from it.
	if got[1].Key != "slack" || got[1].Name != "slack" {
		t.Errorf("second = key=%s name=%s, want slack/slack (key backfilled from name)", got[1].Key, got[1].Name)
	}
}

func TestParseCatalogRejectsGarbage(t *testing.T) {
	if _, err := parseCatalog([]byte("not json")); err == nil {
		t.Fatal("expected parse error on non-JSON")
	}
}

// TestCatalogListAndResolve drives List + Resolve against a stub server,
// covering the happy path + ETag 304 reuse.
func TestCatalogListAndResolve(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(sampleCatalog))
	}))
	defer srv.Close()

	c := &Catalog{url: srv.URL, ttl: 0, hc: srv.Client()} // ttl 0 → always revalidate

	list, err := c.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}

	// Second call with ttl 0 issues a conditional GET; the stub returns 304 and
	// the cache is reused.
	if _, err := c.List(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("want 2 server hits (initial + revalidate), got %d", hits)
	}

	_, url, err := c.Resolve(t.Context(), "gmail", "linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://example.com/gmail-1.2.0-linux-arm64.zip" {
		t.Errorf("resolved url = %s", url)
	}

	if _, _, err := c.Resolve(t.Context(), "gmail", "plan9/foo"); err == nil {
		t.Error("expected error for missing arch")
	}
	if _, _, err := c.Resolve(t.Context(), "nope", "linux/arm64"); err == nil {
		t.Error("expected error for unknown connector")
	}
}
