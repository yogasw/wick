package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
)

// newTraceStore is newStore with trace caps set: 4 KB per event, blobs
// up to blobMax bytes.
func newTraceStore(t *testing.T, blobMax int) (*Store, config.Layout) {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S1", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	return New(Options{
		Layout: layout, SessionID: "S1",
		TraceEventMaxBytes: 4 << 10,
		TraceBlobMaxBytes:  blobMax,
		Now:                func() time.Time { return time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC) },
	}), layout
}

// runClaudeTurn pushes claude stream-json lines through the real parser
// into the store and returns the turn's trace index plus its dir.
func runClaudeTurn(t *testing.T, st *Store, layout config.Layout, frames ...any) (TurnTraceIndex, string) {
	t.Helper()
	p := event.NewClaudeParser()
	frames = append(frames, map[string]any{"type": "result", "subtype": "success", "result": "ok"})
	for _, f := range frames {
		line, _ := json.Marshal(f)
		ev, err := p.Parse(string(line))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	idxs, _ := filepath.Glob(filepath.Join(layout.SessionThinkingDir("S1"), "*", "index.json"))
	if len(idxs) != 1 {
		t.Fatalf("want one trace index, got %v", idxs)
	}
	var idx TurnTraceIndex
	data, _ := os.ReadFile(idxs[0])
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	return idx, filepath.Dir(idxs[0])
}

func toolUse(id, name string, input any) map[string]any {
	return map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
		"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}}
}

func toolResult(id string, content any) map[string]any {
	return map[string]any{"type": "user", "message": map[string]any{"role": "user",
		"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": content}}}}
}

func readPayload(t *testing.T, dir, eventID string) TurnEventPayload {
	t.Helper()
	var ep TurnEventPayload
	data, err := os.ReadFile(filepath.Join(dir, eventID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &ep); err != nil {
		t.Fatal(err)
	}
	return ep
}

func resultRow(t *testing.T, idx TurnTraceIndex) TurnEventIndex {
	t.Helper()
	for _, r := range idx.Events {
		if r.Type == "tool_result" {
			return r
		}
	}
	t.Fatal("no tool_result in trace")
	return TurnEventIndex{}
}

// A 50 KB markdown result: classified on the FULL text, cut to the cap on
// a rune boundary, the index keeps kind + original size without the body.
func TestTraceLargeMarkdownTruncatedOnRuneBoundary(t *testing.T) {
	st, layout := newTraceStore(t, 0)
	md := "# Laporan\n\n" + strings.Repeat("- **baris** dengan é dan 日本語\n", 1500)
	if len(md) < 50<<10 {
		t.Fatalf("fixture too small: %d", len(md))
	}
	idx, dir := runClaudeTurn(t, st, layout, toolUse("t1", "Task", map[string]any{"prompt": "x"}), toolResult("t1", md))
	row := resultRow(t, idx)
	if !row.Large || row.Display == nil || row.Display.Kind != event.KindMarkdown || row.Display.Body != "" {
		t.Fatalf("index row = %+v / %+v", row, row.Display)
	}
	if !row.Display.Truncated || row.Display.OriginalBytes < 50<<10 {
		t.Fatalf("header must say truncated + original size: %+v", row.Display)
	}
	ep := readPayload(t, dir, row.EventID)
	if !ep.Truncated || ep.Display == nil || !ep.Display.Truncated {
		t.Fatalf("payload not marked truncated")
	}
	for name, s := range map[string]string{"text": ep.Text, "body": ep.Display.Body} {
		if len(s) > 4<<10 || !utf8.ValidString(s) {
			t.Errorf("%s: len %d valid %v — must be ≤ cap and valid UTF-8", name, len(s), utf8.ValidString(s))
		}
	}
	if !strings.HasPrefix(ep.Display.Body, "# Laporan") {
		t.Errorf("body not unwrapped: %.30q", ep.Display.Body)
	}
}

// A 50 KB JSON result stays kind json even though its stored body no
// longer parses — the UI falls back to code + banner.
func TestTraceLargeJSONKeepsKind(t *testing.T) {
	st, layout := newTraceStore(t, 0)
	rows := make([]map[string]any, 0, 1100)
	for i := 0; i < 1100; i++ {
		rows = append(rows, map[string]any{"id": i, "name": "row-name-ünïcode", "ok": true})
	}
	js, _ := json.Marshal(rows)
	if len(js) < 50<<10 {
		t.Fatalf("fixture too small: %d", len(js))
	}
	idx, dir := runClaudeTurn(t, st, layout,
		toolUse("t1", "mcp__wick__wick_execute", map[string]any{"tool_id": "conn:x/list"}),
		toolResult("t1", []any{map[string]any{"type": "text", "text": string(js)}}))
	if use := idx.Events[0]; use.Display == nil || use.Display.Kind != event.KindMCP || use.Display.Connector != "x" {
		t.Fatalf("call display = %+v", use.Display)
	}
	row := resultRow(t, idx)
	if row.Display == nil || row.Display.Kind != event.KindJSON || !row.Display.Truncated {
		t.Fatalf("result display = %+v", row.Display)
	}
	ep := readPayload(t, dir, row.EventID)
	if json.Valid([]byte(ep.Display.Body)) || !utf8.ValidString(ep.Display.Body) {
		t.Errorf("expected a cut (invalid) but UTF-8-valid json body")
	}
}

// A 500 KB PNG never gets cut: the bytes go whole to <event>.bin, the
// event text is a short reference, not base64.
func TestTraceImageStoredAsBlob(t *testing.T) {
	st, layout := newTraceStore(t, 0)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 500<<10)...)
	b64 := base64.StdEncoding.EncodeToString(png)
	idx, dir := runClaudeTurn(t, st, layout,
		toolUse("t1", "Read", map[string]any{"file_path": "/w/shot.png"}),
		toolResult("t1", []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": b64}}}))
	row := resultRow(t, idx)
	d := row.Display
	if row.Large || d == nil || d.Kind != event.KindImage || d.Mime != "image/png" || d.BlobRef != row.EventID || d.Name != "shot.png" {
		t.Fatalf("row %+v display %+v", row, d)
	}
	if d.OriginalBytes != len(png) || d.Truncated || d.TooLarge {
		t.Fatalf("size/flags wrong: %+v", d)
	}
	got, err := os.ReadFile(filepath.Join(dir, d.BlobRef+".bin"))
	if err != nil || !bytes.Equal(got, png) {
		t.Fatalf("blob mismatch (err %v, %d bytes)", err, len(got))
	}
	if strings.Contains(row.Text, b64[:400]) || len(row.Text) > 400 || !strings.Contains(row.Text, "image/png") {
		t.Fatalf("event text must be a reference, got %d bytes: %.120q", len(row.Text), row.Text)
	}
}

// Over trace_blob_max_mb: info only, no blob, still no base64 in the text.
func TestTraceImageOverBlobCap(t *testing.T) {
	st, layout := newTraceStore(t, 64<<10)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 100<<10)...)
	idx, dir := runClaudeTurn(t, st, layout, toolUse("t1", "Bash", map[string]any{"command": "cat a.png | base64"}),
		toolResult("t1", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png)))
	d := resultRow(t, idx).Display
	if d == nil || d.Kind != event.KindImage || !d.TooLarge || d.BlobRef != "" {
		t.Fatalf("display = %+v", d)
	}
	if bins, _ := filepath.Glob(filepath.Join(dir, "*.bin")); len(bins) != 0 {
		t.Fatalf("no blob expected, got %v", bins)
	}
	if txt := resultRow(t, idx).Text; !strings.Contains(txt, "too large") || len(txt) > 400 {
		t.Fatalf("text = %.200q", txt)
	}
}

// A crash mid-turn recovers the blob from inflight.jsonl even though Blob
// itself is never serialized.
func TestRecoverInflightRestoresBlob(t *testing.T) {
	st, layout := newTraceStore(t, 0)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{3}, 2048)...)
	p := event.NewClaudeParser()
	for _, f := range []any{
		toolUse("t1", "Read", map[string]any{"file_path": "/w/a.png"}),
		toolResult("t1", []any{map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(png), "mimeType": "image/png"}}),
	} {
		line, _ := json.Marshal(f)
		ev, _ := p.Parse(string(line))
		if _, err := st.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	ok, err := RecoverInflight(layout, "S1", "main", "claude/c", nil)
	if err != nil || !ok {
		t.Fatalf("recover: %v %v", ok, err)
	}
	bins, _ := filepath.Glob(filepath.Join(layout.SessionThinkingDir("S1"), "*", "e1.bin"))
	if len(bins) != 1 {
		t.Fatalf("blob not restored: %v", bins)
	}
	if got, _ := os.ReadFile(bins[0]); !bytes.Equal(got, png) {
		t.Fatal("restored blob differs")
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := "aé日" // 1 + 2 + 3 bytes
	for n, want := range map[int]string{0: "", 1: "a", 2: "a", 3: "aé", 4: "aé", 5: "aé", 6: "aé日", 9: "aé日"} {
		if got := truncateUTF8(s, n); got != want {
			t.Errorf("truncateUTF8(%d) = %q, want %q", n, got, want)
		}
	}
}
