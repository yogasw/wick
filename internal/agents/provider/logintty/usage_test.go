package logintty

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestReadUsageClaude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok-123"}}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/usage" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
			t.Errorf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 42.5, "resets_at": "2026-09-07T12:00:00Z"},
			"seven_day": {"utilization": 80, "resets_at": "2026-09-10T00:00:00Z"},
			"extra": "ignored"
		}`))
	}))
	defer srv.Close()

	old := anthropicAPIBase
	anthropicAPIBase = srv.URL
	defer func() { anthropicAPIBase = old }()

	windows, err := ReadUsage(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %+v", windows)
	}
	if windows[0].Key != "five_hour" || windows[0].Utilization != 42.5 {
		t.Fatalf("first window = %+v", windows[0])
	}
	if want := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC); !windows[0].ResetsAt.Equal(want) {
		t.Fatalf("ResetsAt = %v", windows[0].ResetsAt)
	}
	if windows[1].Key != "seven_day" || windows[1].Utilization != 80 {
		t.Fatalf("second window = %+v", windows[1])
	}
}

func TestReadUsageClaudeNoCredentials(t *testing.T) {
	if _, err := ReadUsage(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}); err == nil {
		t.Fatal("must error without credentials")
	}
}

func TestReadUsageUnsupportedTypes(t *testing.T) {
	if _, err := ReadUsage(provider.TypeCodex, nil); err != ErrUsageUnsupported {
		t.Fatalf("codex err = %v, want ErrUsageUnsupported", err)
	}
	if _, err := ReadUsage(provider.TypeWick, nil); err != ErrUsageUnsupported {
		t.Fatalf("wick err = %v, want ErrUsageUnsupported", err)
	}
}
