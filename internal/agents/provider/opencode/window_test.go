package opencode

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCachedWindow(t *testing.T) {
	dir := t.TempDir()
	path := modelsCachePath([]string{"XDG_CACHE_HOME=" + dir})
	if want := filepath.Join(dir, "opencode", "models.json"); path != want {
		t.Fatalf("cache path %q, want %q", path, want)
	}
	if got := cachedWindow(path, "opencode/kimi-k3"); got != 0 {
		t.Fatalf("no file: %d", got)
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	write := func(s string, mod time.Time) {
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(path, mod, mod)
	}
	t0 := time.Now().Add(-time.Hour)
	write(`{"opencode":{"models":{"kimi-k3":{"limit":{"context":262144,"output":32768}},"nolimit":{}}}}`, t0)
	for model, want := range map[string]int{"opencode/kimi-k3": 262144, "opencode/nolimit": 0, "anthropic/x": 0, "kimi-k3": 0} {
		if got := cachedWindow(path, model); got != want {
			t.Errorf("%s: %d, want %d", model, got, want)
		}
	}
	// A rewritten file (new mtime) is read again.
	write(`{"opencode":{"models":{"kimi-k3":{"limit":{"context":1000}}}}}`, t0.Add(time.Minute))
	if got := cachedWindow(path, "opencode/kimi-k3"); got != 1000 {
		t.Fatalf("after rewrite: %d, want 1000", got)
	}
}

func TestRunStreamGetsContextLine(t *testing.T) {
	b, _ := io.ReadAll(withContextLine(io.NopCloser(strings.NewReader(`{"type":"step_start"}`+"\n")), 262144))
	if want := "{\"type\":\"context\",\"window\":262144}\n{\"type\":\"step_start\"}\n"; string(b) != want {
		t.Fatalf("stream %q, want %q", b, want)
	}
}
