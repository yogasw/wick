package omp

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelsDBWindow(t *testing.T) {
	db := filepath.Join(t.TempDir(), "models.db")
	writeDB(t, db,
		`CREATE TABLE model_cache (provider_id TEXT PRIMARY KEY, version INTEGER NOT NULL, updated_at INTEGER NOT NULL, authoritative INTEGER NOT NULL DEFAULT 0, models TEXT NOT NULL)`,
		`INSERT INTO model_cache VALUES ('openai-codex:0.159.0', 13, 1, 1, '{"models":[{"id":"gpt-5.5","provider":"openai-codex","contextWindow":272000}]}')`,
		`INSERT INTO model_cache VALUES ('anthropic', 13, 1, 1, '[{"id":"claude","provider":"anthropic","contextWindow":200000}]')`,
		`INSERT INTO model_cache VALUES ('xai', 14, 1, 1, '[{"id":"grok","provider":"xai","contextWindow":9}]')`, // unknown version: not read
	)
	ctx := context.Background()
	for sel, want := range map[string]int{
		"openai-codex/gpt-5.5": 272000,
		"anthropic/claude":     200000,
		"anthropic/other":      0,
		"xai/grok":             0,
		"claude":               0,
	} {
		if got := modelsDBWindow(ctx, db, sel); got != want {
			t.Errorf("%s: window %d, want %d", sel, got, want)
		}
	}
	if got := modelsDBWindow(ctx, filepath.Join(t.TempDir(), "none.db"), "anthropic/claude"); got != 0 {
		t.Errorf("missing file: %d", got)
	}
}

func TestModelArgAndContextLine(t *testing.T) {
	if got := modelArg([]string{"-p", "--model", "a/b", "--model=c/d"}); got != "c/d" {
		t.Fatalf("last --model wins, got %q", got)
	}
	if got := modelArg([]string{"-p"}); got != "" {
		t.Fatalf("no model: %q", got)
	}
	r := withContextLine(io.NopCloser(strings.NewReader(`{"type":"session"}`+"\n")), 200000)
	b, _ := io.ReadAll(r)
	if want := "{\"type\":\"context\",\"window\":200000}\n{\"type\":\"session\"}\n"; string(b) != want {
		t.Fatalf("stream %q, want %q", b, want)
	}
	if r := withContextLine(io.NopCloser(strings.NewReader("x")), 0); r == nil {
		t.Fatal("nil reader")
	} else if b, _ := io.ReadAll(r); string(b) != "x" {
		t.Fatalf("no window must leave the stream alone, got %q", b)
	}
}
