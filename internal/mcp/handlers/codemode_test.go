package handlers

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestExecuteContentHintsCodeModeOnBigResults(t *testing.T) {
	if c := ExecuteContent(`{"ok":true}`); len(c) != 1 {
		t.Fatalf("small result got a hint: %+v", c)
	}
	big := `{"rows":"` + strings.Repeat("x", CodeModeHintBytes) + `"}`
	c := ExecuteContent(big)
	if len(c) != 2 || c[0].Text != big || !strings.Contains(c[1].Text, "wick_run_code") {
		t.Fatalf("big result: %d parts, hint=%q", len(c), c[len(c)-1].Text)
	}
}

func TestTSCall(t *testing.T) {
	in := []entity.Config{
		{Key: "channel", Required: true},
		{Key: "limit", Type: "number"},
		{Key: "mode", Type: "dropdown", Options: "fast|full"},
		{Key: "dry-run", Type: "checkbox"},
	}
	got := TSCall("conn:acme-1/list_rows", in)
	want := `await wick.call("conn:acme-1/list_rows", { channel: string; limit?: number; mode?: "fast" | "full"; "dry-run"?: boolean })`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if TSCall("conn:x/ping", nil) != `await wick.call("conn:x/ping", {})` {
		t.Fatal(TSCall("conn:x/ping", nil))
	}
}
