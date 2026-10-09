package logintty

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/savedresets"
)

// Each provider file registers its own reader in the base package.
func TestProvidersRegisterSavedResetsReaders(t *testing.T) {
	for _, builtin := range []provider.Type{provider.TypeClaude, provider.TypeCodex} {
		if _, ok := savedresets.ReaderFor(string(builtin)); !ok {
			t.Errorf("%s registers no reader", builtin)
		}
	}
	if _, ok := savedresets.ReaderFor(string(provider.TypeGemini)); ok {
		t.Error("gemini must have no reader")
	}
}

func TestParseClaudeSavedResets(t *testing.T) {
	r := parseClaudeSavedResets([]byte(`{
		"eligible": true, "at_limit": false,
		"grants": [
			{"id":"g2","label":"Second","resets_total":1,"resets_left":1,"ends_at":"2026-11-02T00:00:00Z","usable_now":false,"use_requires_limit":true},
			{"id":"g1","label":"First","resets_total":2,"resets_left":1,"starts_at":"2026-10-01T00:00:00Z","ends_at":"2026-10-30T00:00:00Z","usable_now":true},
			{"id":"g0","label":"Spent","resets_total":1,"resets_left":0,"ends_at":"2026-10-20T00:00:00Z"}
		],
		"cooldown_until": "2026-10-11T14:00:00Z"
	}`))
	if r == nil || !r.Supported || r.Available != 2 || r.Total != 4 {
		t.Fatalf("got %+v", r)
	}
	if len(r.Items) != 2 || r.Items[0].ID != "g1" || r.Items[1].ID != "g2" {
		t.Fatalf("items = %+v, want g1 then g2 (soonest first, spent dropped)", r.Items)
	}
	if !r.Items[0].UsableNow || !r.Items[1].RequiresLimit {
		t.Errorf("flags lost: %+v", r.Items)
	}
	if !r.CooldownUntil.Equal(time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("cooldown = %v", r.CooldownUntil)
	}

	off := parseClaudeSavedResets([]byte(`{"eligible":false,"ineligible_reason":"flag_off","grants":[]}`))
	if off == nil || !off.Supported || off.Available != 0 || off.Note != "Not available for this account." {
		t.Fatalf("ineligible = %+v", off)
	}
	if parseClaudeSavedResets(nil) != nil || parseClaudeSavedResets([]byte(`null`)) != nil {
		t.Error("absent block must give nil")
	}
}

// The usage call carries cedar_ember; the reader serves it without a
// request of its own.
func TestClaudeSavedResetsPiggybackOnUsage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"synthetic"}}`)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("cedar_ember") != "1" {
			t.Errorf("query = %q, want cedar_ember=1", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":5},
			"cedar_ember":{"eligible":true,"grants":[{"id":"g","label":"Reset","resets_total":1,"resets_left":1,"ends_at":"2026-10-30T00:00:00Z","usable_now":true}]}}`))
	}))
	defer srv.Close()
	old := anthropicAPIBase
	anthropicAPIBase = srv.URL
	defer func() { anthropicAPIBase = old }()

	env := []string{"CLAUDE_CONFIG_DIR=" + dir}
	ws, err := ReadUsage(provider.TypeClaude, env)
	if err != nil || len(ws) != 1 || ws[0].Key != "five_hour" {
		t.Fatalf("usage: ws=%+v err=%v (cedar_ember must not become a window)", ws, err)
	}
	r, err := savedresets.Read(string(provider.TypeClaude), env)
	if err != nil || r.Available != 1 || len(r.Items) != 1 {
		t.Fatalf("saved resets: r=%+v err=%v", r, err)
	}
	if calls != 1 {
		t.Errorf("upstream calls = %d, want 1", calls)
	}
	if _, err := savedresets.Read(string(provider.TypeClaude), []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}); err == nil {
		t.Error("a dir never probed must report no reading")
	}
}

func TestCodexSavedResets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth.json"), `{"tokens":{"access_token":"synthetic","account_id":"acct-1"}}`)
	mux := http.NewServeMux()
	mux.HandleFunc("/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("ChatGPT-Account-Id") != "acct-1" {
			t.Errorf("account header = %q", r.Header.Get("ChatGPT-Account-Id"))
		}
		_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit_reset_credits":{"available_count":2}}`))
	})
	mux.HandleFunc("/credits", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"credits":[
			{"id":"c2","reset_type":"full_reset","status":"available","expires_at":"2099-11-02T00:00:00Z","title":"Full reset (Weekly + 5 hr)"},
			{"id":"c1","reset_type":"full_reset","status":"available","expires_at":"2099-10-14T00:00:00Z","title":""},
			{"id":"c0","status":"consumed","expires_at":"2099-10-01T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	pu, pc := chatgptUsageURL, chatgptResetCreditsURL
	chatgptUsageURL, chatgptResetCreditsURL = srv.URL+"/usage", srv.URL+"/credits"
	t.Cleanup(func() { chatgptUsageURL, chatgptResetCreditsURL = pu, pc })

	r, err := savedresets.Read(string(provider.TypeCodex), []string{"CODEX_HOME=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Supported || r.Available != 2 || len(r.Items) != 2 {
		t.Fatalf("got %+v", r)
	}
	if r.Items[0].ID != "c1" || r.Items[0].Label != "full reset" || r.Items[1].Label != "Full reset (Weekly + 5 hr)" {
		t.Errorf("items = %+v", r.Items)
	}
}

func TestParseCodexResetCountAbsent(t *testing.T) {
	r, err := parseCodexResetCount([]byte(`{"plan_type":"free"}`))
	if err != nil || !r.Supported || r.Available != 0 || r.Note == "" {
		t.Fatalf("got %+v err=%v", r, err)
	}
	r, err = parseCodexResetCount([]byte(`{"rate_limit_reset_credits":{"available_count":0}}`))
	if err != nil || r.Available != 0 || r.Note != "" {
		t.Fatalf("zero count: %+v err=%v", r, err)
	}
}

func TestCodexSavedResetsNoLogin(t *testing.T) {
	if _, err := savedresets.Read(string(provider.TypeCodex), []string{"CODEX_HOME=" + t.TempDir()}); err == nil {
		t.Fatal("no auth.json must error")
	}
}
