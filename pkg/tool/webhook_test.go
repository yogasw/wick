package tool

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// cfgStub is a ConfigReader backed by a map keyed "owner/key".
type cfgStub map[string]string

func (c cfgStub) GetOwned(owner, key string) string { return c[owner+"/"+key] }
func (c cfgStub) Missing(string) []string           { return nil }

func newCtx(body string, cfg ConfigReader) (*WebhookCtx, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodPost, "/tools/t/webhook/x", strings.NewReader(body))
	rec := httptest.NewRecorder()
	return NewWebhookCtx(rec, req, Tool{Key: "t", Path: "/tools/t"}, cfg), rec
}

// TestCfgBoolAcceptedValues pins the documented parse behaviour. "yes" and
// "on" read as false: strconv.ParseBool rejects them, and the doc comment
// used to claim otherwise.
func TestCfgBoolAcceptedValues(t *testing.T) {
	cases := map[string]bool{
		"true": true, "True": true, "TRUE": true, "t": true, "T": true, "1": true,
		"false": false, "False": false, "FALSE": false, "f": false, "0": false,
		// Rejected by ParseBool, so false — not true, as the docs once said.
		"yes": false, "on": false, "no": false, "off": false, "": false, "junk": false,
	}
	for stored, want := range cases {
		c, _ := newCtx("", cfgStub{"t/flag": stored})
		if got := c.CfgBool("flag"); got != want {
			t.Errorf("CfgBool(%q) = %v, want %v", stored, got, want)
		}
	}
}

// TestBodyRejectsOverCap is the security-relevant case: the read happens
// before any signature can be verified, so an unbounded body would be a
// memory-exhaustion vector on an endpoint that answers without a login.
func TestBodyRejectsOverCap(t *testing.T) {
	c, _ := newCtx(strings.Repeat("a", 2048), nil)
	c.SetMaxBody(1024)

	got, err := c.Body()
	if err == nil {
		t.Fatalf("read %d bytes with no error; the cap did not apply", len(got))
	}
	if int64(len(got)) > 1024 {
		t.Fatalf("read %d bytes, cap was 1024", len(got))
	}
}

func TestBodyAllowsUpToCap(t *testing.T) {
	payload := strings.Repeat("a", 1024)
	c, _ := newCtx(payload, nil)
	c.SetMaxBody(1024)

	got, err := c.Body()
	if err != nil {
		t.Fatalf("body at exactly the cap was rejected: %s", err.Error())
	}
	if string(got) != payload {
		t.Fatalf("read %d bytes, want %d", len(got), len(payload))
	}
}

func TestBindJSONRejectsOverCap(t *testing.T) {
	// A syntactically valid but oversized payload: the cap must bite
	// before the decoder finishes, otherwise BindJSON is a second
	// unbounded read alongside Body.
	big := `{"v":"` + strings.Repeat("a", 4096) + `"}`
	c, _ := newCtx(big, nil)
	c.SetMaxBody(512)

	var v map[string]string
	if err := c.BindJSON(&v); err == nil {
		t.Fatal("BindJSON accepted a payload past the cap")
	}
}

// TestDefaultMaxBodyApplied pins that a handler gets the cap without
// having to opt in — the whole point is that forgetting is safe.
func TestDefaultMaxBodyApplied(t *testing.T) {
	c, _ := newCtx("x", nil)
	if c.maxBody != DefaultMaxBodyBytes {
		t.Fatalf("maxBody = %d, want DefaultMaxBodyBytes (%d)", c.maxBody, DefaultMaxBodyBytes)
	}
}

// TestSetMaxBodyRejectsNonPositive pins that there is no way to ask for an
// unlimited read: 0 and negatives fall back to the default.
func TestSetMaxBodyRejectsNonPositive(t *testing.T) {
	for _, n := range []int64{0, -1, -1 << 40} {
		c, _ := newCtx("x", nil)
		c.SetMaxBody(n)
		if c.maxBody != DefaultMaxBodyBytes {
			t.Errorf("SetMaxBody(%d) left maxBody at %d, want the default", n, c.maxBody)
		}
	}
}

// TestJSONWritesStatusAndBody covers the ordinary path alongside the
// encode-failure case below.
func TestJSONWritesStatusAndBody(t *testing.T) {
	c, rec := newCtx("", nil)
	c.JSON(http.StatusAccepted, map[string]string{"status": "ok"})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %s", err.Error())
	}
	if got["status"] != "ok" {
		t.Fatalf("body %v", got)
	}
}

// TestJSONLogsEncodeFailure pins that an unmarshalable value is reported
// rather than swallowed. The status is already on the wire at that point,
// so a log line is the only signal available.
func TestJSONLogsEncodeFailure(t *testing.T) {
	var logged bytes.Buffer
	restore := swapLogOutput(&logged)
	defer restore()

	c, rec := newCtx("", nil)
	// A channel cannot be marshalled to JSON.
	c.JSON(http.StatusOK, map[string]any{"bad": make(chan int)})

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — the header is written before encoding", rec.Code)
	}
	if !strings.Contains(logged.String(), "encode failed") {
		t.Fatalf("encode failure was not logged; log was %q", logged.String())
	}
}

// TestErrorIsJSON pins that a webhook refusal stays machine-readable —
// unlike Ctx.Error, which writes plain text for a browser.
func TestErrorIsJSON(t *testing.T) {
	c, rec := newCtx("", nil)
	c.Error(http.StatusUnauthorized, "bad signature")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("error body was not JSON: %s (%s)", err.Error(), rec.Body.String())
	}
	if got["error"] != "bad signature" {
		t.Fatalf("body %v", got)
	}
}

// swapLogOutput redirects the standard logger to w and returns a function
// that restores the previous destination. Used to assert on log output
// without leaking the redirection into other tests.
func swapLogOutput(w *bytes.Buffer) func() {
	prevFlags := log.Flags()
	log.SetOutput(w)
	log.SetFlags(0)
	return func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(prevFlags)
	}
}
