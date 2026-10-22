package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// mrServer answers user lookups for "lead" (id 17) and "dev" (id 5) and
// records the body of the merge request create call.
func mrServer(t *testing.T, created *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users":
			ids := map[string]int{"lead": 17, "dev": 5}
			if id, ok := ids[r.URL.Query().Get("username")]; ok {
				_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": id}})
				return
			}
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/v4/projects/acme%2Fweb/merge_requests":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, created)
			_, _ = io.WriteString(w, `{"iid":3,"reviewers":[{"username":"lead"}]}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
		}
	}))
}

func mrCtx(base, defaults string, input map[string]string) *connector.Ctx {
	in := map[string]string{"project": "acme/web", "source_branch": "a", "target_branch": "main", "title": "t"}
	for k, v := range input {
		in[k] = v
	}
	return connector.NewCtx(context.Background(), "test", map[string]string{"base_url": base, "token": "tok", "default_reviewers": defaults}, in, http.DefaultClient, nil, nil)
}

func reviewerIDsOf(body map[string]any) []float64 {
	var out []float64
	list, _ := body["reviewer_ids"].([]any)
	for _, v := range list {
		f, _ := v.(float64)
		out = append(out, f)
	}
	return out
}

func TestCreateMR_AddsDefaultReviewers(t *testing.T) {
	var created map[string]any
	srv := mrServer(t, &created)
	defer srv.Close()

	out, err := createMR(mrCtx(srv.URL, "lead", map[string]string{"reviewers": "@dev, 17"}))
	if err != nil {
		t.Fatal(err)
	}
	got := reviewerIDsOf(created)
	if len(got) != 2 || got[0] != 5 || got[1] != 17 {
		t.Fatalf("reviewer_ids = %v, want [5 17] (lead once)", created["reviewer_ids"])
	}
	if _, ok := out.(map[string]any)["reviewers"]; !ok {
		t.Fatal("create result must carry reviewers so the caller can verify them")
	}
}

func TestCreateMR_DefaultReviewersWithoutInput(t *testing.T) {
	var created map[string]any
	srv := mrServer(t, &created)
	defer srv.Close()

	if _, err := createMR(mrCtx(srv.URL, "lead", nil)); err != nil {
		t.Fatal(err)
	}
	if got := reviewerIDsOf(created); len(got) != 1 || got[0] != 17 {
		t.Fatalf("reviewer_ids = %v, want [17]", created["reviewer_ids"])
	}
}

func TestCreateMR_SkipDefaultReviewers(t *testing.T) {
	var created map[string]any
	srv := mrServer(t, &created)
	defer srv.Close()

	if _, err := createMR(mrCtx(srv.URL, "lead", map[string]string{"skip_default_reviewers": "true"})); err != nil {
		t.Fatal(err)
	}
	if _, ok := created["reviewer_ids"]; ok {
		t.Fatalf("reviewer_ids = %v, want none when skip_default_reviewers is set", created["reviewer_ids"])
	}
}

func TestCreateMR_UnknownReviewerFails(t *testing.T) {
	var created map[string]any
	srv := mrServer(t, &created)
	defer srv.Close()

	if _, err := createMR(mrCtx(srv.URL, "nobody", nil)); err == nil {
		t.Fatal("an unknown reviewer username must fail instead of opening the MR without it")
	}
	if created != nil {
		t.Fatal("the MR must not be created when a reviewer cannot be resolved")
	}
}
