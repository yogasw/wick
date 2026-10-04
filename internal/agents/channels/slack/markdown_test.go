package slack

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/pkg/slackmd"
)

const mdTable = "Ringkasan akses:\n\n| Area | Bisa apa | Contoh |\n|---|---|---|\n| Logs | cek error | Loki |\n| Tiket | buat tiket | Notion |"

// mdCall is one recorded Slack Web API call.
type mdCall struct {
	method string
	vals   url.Values
}

// mdFakeSlack records chat.postMessage and chat.update so a test can assert
// on the exact payload a thread would receive.
type mdFakeSlack struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []mdCall
}

func newMDFakeSlack(t *testing.T) *mdFakeSlack {
	t.Helper()
	f := &mdFakeSlack{}
	record := func(method string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			f.mu.Lock()
			f.calls = append(f.calls, mdCall{method, vals})
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1700000000.000300"}`))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat.postMessage", record("post"))
	mux.HandleFunc("/chat.update", record("update"))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *mdFakeSlack) client() *slackgo.Client {
	return slackgo.New("xoxb-test", slackgo.OptionAPIURL(f.srv.URL+"/"))
}

func (f *mdFakeSlack) got() []mdCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mdCall(nil), f.calls...)
}

// markdownBlock returns the text of the single markdown block in a call, or
// fails when the call does not carry exactly that.
func markdownBlock(t *testing.T, c mdCall) string {
	t.Helper()
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(c.vals.Get("blocks")), &blocks); err != nil {
		t.Fatalf("%s: blocks not valid JSON: %v (%q)", c.method, err, c.vals.Get("blocks"))
	}
	if len(blocks) != 1 || blocks[0]["type"] != "markdown" {
		t.Fatalf("%s: want one markdown block, got %v", c.method, blocks)
	}
	text, _ := blocks[0]["text"].(string)
	return text
}

func newMDChannel(f *mdFakeSlack, tr *turn) *Channel {
	return &Channel{api: f.client(), turns: map[string]*turn{"slack-t1": tr}}
}

func TestPostChunkedTableUsesMarkdownBlock(t *testing.T) {
	f := newMDFakeSlack(t)
	c := newMDChannel(f, &turn{})
	c.postChunked("C1", "1700000000.000100", mdTable)

	calls := f.got()
	if len(calls) != 1 {
		t.Fatalf("want 1 post, got %d", len(calls))
	}
	if got := markdownBlock(t, calls[0]); got != mdTable {
		t.Fatalf("markdown block text = %q, want the original markdown", got)
	}
	if got, want := calls[0].vals.Get("text"), slackmd.Fallback(mdTable); got != want {
		t.Fatalf("fallback text = %q, want %q", got, want)
	}
	if strings.Contains(calls[0].vals.Get("text"), "|---") {
		t.Fatal("fallback text must not carry table markup")
	}
	t.Logf("chat.postMessage text=%q blocks=%s", calls[0].vals.Get("text"), calls[0].vals.Get("blocks"))
}

func TestPostChunkedPlainTextKeepsTextPath(t *testing.T) {
	f := newMDFakeSlack(t)
	c := newMDChannel(f, &turn{})
	plain := "Deploy selesai, *semua* aman. Detail: <https://example.com|log>"
	c.postChunked("C1", "1700000000.000100", plain)

	calls := f.got()
	if len(calls) != 1 {
		t.Fatalf("want 1 post, got %d", len(calls))
	}
	if b := calls[0].vals.Get("blocks"); b != "" {
		t.Fatalf("plain text must not use blocks, got %q", b)
	}
	if got := calls[0].vals.Get("text"); got != plain {
		t.Fatalf("text = %q, want unchanged %q", got, plain)
	}
}

func TestStreamingPostAndEditUseMarkdownBlock(t *testing.T) {
	f := newMDFakeSlack(t)
	tr := &turn{channelID: "C1", threadTS: "1700000000.000100", running: true}
	tr.buf.WriteString("Ringkasan akses:\n\n| Area | Bisa apa |\n|---|---|\n| Logs | cek error |")
	c := newMDChannel(f, tr)

	c.flushLiveMessage("slack-t1") // first flush posts the live message
	tr.buf.WriteString("\n| Tiket | buat tiket |")
	c.flushLiveMessage("slack-t1") // later flush edits it in place

	calls := f.got()
	if len(calls) != 2 || calls[0].method != "post" || calls[1].method != "update" {
		t.Fatalf("want post then update, got %+v", calls)
	}
	if got := markdownBlock(t, calls[0]); !strings.HasSuffix(got, "| Logs | cek error |") {
		t.Fatalf("live post block = %q", got)
	}
	if got := markdownBlock(t, calls[1]); !strings.HasSuffix(got, "| Tiket | buat tiket |") {
		t.Fatalf("edit block = %q", got)
	}
}

func TestFinalizeEditUsesMarkdownBlock(t *testing.T) {
	f := newMDFakeSlack(t)
	tr := &turn{channelID: "C1", threadTS: "1700000000.000100"}
	c := newMDChannel(f, tr)

	c.finalizeReply("slack-t1", "C1", "1700000000.000100", mdTable, "1700000000.000200", "Ringkasan akses:")

	calls := f.got()
	if len(calls) != 1 || calls[0].method != "update" {
		t.Fatalf("want a single chat.update, got %+v", calls)
	}
	if calls[0].vals.Get("ts") != "1700000000.000200" {
		t.Fatalf("update targets ts %q, want the live message", calls[0].vals.Get("ts"))
	}
	if got := markdownBlock(t, calls[0]); got != mdTable {
		t.Fatalf("edit block = %q", got)
	}
}

// A live message that already went out as a markdown block stays a block
// when the final text no longer needs one, so the old block is replaced
// instead of lingering next to the new text.
func TestFinalizeEditKeepsBlockOnceMessageWasBlock(t *testing.T) {
	f := newMDFakeSlack(t)
	c := newMDChannel(f, &turn{channelID: "C1", threadTS: "1700000000.000100"})

	c.finalizeReply("slack-t1", "C1", "1700000000.000100", "Selesai.", "1700000000.000200", "## Draft\nsedang menulis")

	calls := f.got()
	if len(calls) != 1 || calls[0].method != "update" {
		t.Fatalf("want a single chat.update, got %+v", calls)
	}
	if got := markdownBlock(t, calls[0]); got != "Selesai." {
		t.Fatalf("edit block = %q", got)
	}
}

func TestReconcilePlanMarkdownUsesBlockLimit(t *testing.T) {
	// Above the mrkdwn chunk size but under the markdown block limit: one
	// message, not two.
	var b strings.Builder
	b.WriteString("| No | Nama |\n|---|---|\n")
	for b.Len() < maxSlackChunk+2000 {
		b.WriteString("| 1 | baris tabel |\n")
	}
	mid := strings.TrimRight(b.String(), "\n")
	if p := reconcilePlan(mid, "1.0", ""); len(p.continuations) != 0 || p.first != mid {
		t.Fatalf("table under the block limit must stay one message, got %d continuations", len(p.continuations))
	}

	// Over the block limit: split between rows, every piece still a table.
	for b.Len() < slackmd.MaxBlockChars+3000 {
		b.WriteString("| 1 | baris tabel |\n")
	}
	big := strings.TrimRight(b.String(), "\n")
	p := reconcilePlan(big, "1.0", "")
	if len(p.continuations) == 0 {
		t.Fatal("table over the block limit must split")
	}
	for _, chunk := range append([]string{p.first}, p.continuations...) {
		if len(chunk) > slackmd.MaxBlockChars {
			t.Fatalf("chunk over the block limit: %d", len(chunk))
		}
		if !strings.HasPrefix(chunk, "| No | Nama |\n|---|---|\n") {
			t.Fatalf("chunk lost its table header: %q", chunk[:30])
		}
		if !strings.HasSuffix(chunk, "| 1 | baris tabel |") {
			t.Fatalf("chunk cut mid-row: %q", chunk[len(chunk)-30:])
		}
	}
}
