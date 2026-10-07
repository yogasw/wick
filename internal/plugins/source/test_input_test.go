package source

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func countSources(t *testing.T, m *Manager) int64 {
	t.Helper()
	var n int64
	if err := m.DB.Model(&entity.PluginSource{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// stepsUpTo reports whether steps 1..n all passed, with the first failure.
func stepsUpTo(steps []Step, n int) (bool, string) {
	for _, st := range steps[:n] {
		if st.Status != "ok" {
			return false, st.Name + ": " + st.Message
		}
	}
	return true, ""
}

// TestTestInputStoresNothing covers the Add form's Test button on a
// plugins.json link and a direct zip link: same checks as a saved source,
// and no row written.
func TestTestInputStoresNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			zb, _ := buildZip(t, "echo", "tool", "1.0.0")
			_, _ = w.Write(zb)
			return
		}
		_, zs := buildZip(t, "echo", "tool", "1.0.0")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"key": "echo", "kind": "tool", "version": "1.0.0",
			"assets": map[string]any{HostOSArch(): map[string]string{"url": "files/" + zipName("echo", "1.0.0"), "zip_sha256": zs}}}})
	}))
	defer srv.Close()
	m := &Manager{DB: newDB(t), Client: &Client{HTTP: http.DefaultClient}, Install: tmpRoot(t)}

	for _, u := range []string{srv.URL + "/plugins.json", srv.URL + "/dl/" + zipName("echo", "1.0.0")} {
		steps, err := m.TestInput(context.Background(), "", SourceInput{Type: TypeURL, URL: u}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if ok, why := stepsUpTo(steps, 5); !ok {
			t.Fatalf("%s: %s", u, why)
		}
		if !strings.Contains(steps[2].Message, "echo v1.0.0") {
			t.Fatalf("index step should list keys: %q", steps[2].Message)
		}
	}
	if n := countSources(t, m); n != 0 {
		t.Fatalf("Test stored %d source(s)", n)
	}
}

// TestAddValidatesBeforeSaving: a source that fails the check is not stored
// (nothing to delete before retrying); a good one is, and the PAT never
// appears in any step message.
func TestAddValidatesBeforeSaving(t *testing.T) {
	f := newFakeGitHub(t, true, "ghp_test", rel{key: "alpha", kind: "tool", ver: "1.0.0"})
	m := &Manager{DB: newDB(t), Client: f.client(), Install: tmpRoot(t)}
	ctx := context.Background()

	_, steps, err := m.Add(ctx, SourceInput{Type: TypeGitHub, Repo: "acme/plugins", Private: true}, "admin", nil)
	var verr *ValidationError
	if !errors.As(err, &verr) || steps[1].Status != "fail" || !strings.Contains(steps[1].Message, "needs a PAT") {
		t.Fatalf("private repo without PAT: err=%v steps=%+v", err, steps)
	}
	_, _, err = m.Add(ctx, SourceInput{Type: TypeGitHub, Repo: "acme/plugins", Private: true, PAT: "ghp_wrong"}, "admin", nil)
	if !errors.As(err, &verr) || !strings.Contains(err.Error(), "PAT rejected") {
		t.Fatalf("wrong PAT: %v", err)
	}
	if n := countSources(t, m); n != 0 {
		t.Fatalf("failed Add stored %d source(s)", n)
	}

	s, steps, err := m.Add(ctx, SourceInput{Type: TypeGitHub, Repo: "acme/plugins", Private: true, PAT: "ghp_test"}, "admin", nil)
	if err != nil || s == nil {
		t.Fatalf("good Add: %v %+v", err, steps)
	}
	if n := countSources(t, m); n != 1 {
		t.Fatalf("good Add stored %d source(s), want 1", n)
	}
	for _, st := range steps {
		if strings.Contains(st.Message, "ghp_") {
			t.Fatalf("step %d leaks the PAT: %q", st.N, st.Message)
		}
	}
}

// TestGitHubRateLimitMessage: a 403 with X-RateLimit-Remaining: 0 is named
// as a rate limit, not as "not found or private".
func TestGitHubRateLimitMessage(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		http.Error(w, "API rate limit exceeded", http.StatusForbidden)
	}))
	defer api.Close()
	m := &Manager{DB: newDB(t), Client: &Client{HTTP: http.DefaultClient, GitHubAPI: api.URL}}
	steps, err := m.TestInput(context.Background(), "", SourceInput{Type: TypeGitHub, Repo: "acme/plugins"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if steps[1].Status != "fail" || !strings.Contains(steps[1].Message, "rate limit") {
		t.Fatalf("auth step = %+v", steps[1])
	}
}
