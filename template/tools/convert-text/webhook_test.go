package converttext

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

const testKey = "convert-text"

// stubCfg is a ConfigReader backed by a map keyed "owner/key".
type stubCfg map[string]string

func (s stubCfg) GetOwned(owner, key string) string { return s[owner+"/"+key] }
func (s stubCfg) Missing(string) []string           { return nil }

func sign(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// call drives webhookConvert against a config with the given toggle and
// secret, signing with signWith (pass "" to omit the header).
func call(body, signWith, enabled, secret string) *httptest.ResponseRecorder {
	cfg := stubCfg{
		testKey + "/webhook_enabled": enabled,
		testKey + "/webhook_secret":  secret,
	}
	req := httptest.NewRequest(http.MethodPost, "/tools/"+testKey+"/webhook/convert", strings.NewReader(body))
	if signWith != "" {
		req.Header.Set(signatureHeader, sign(body, signWith))
	}
	rec := httptest.NewRecorder()
	webhookConvert(tool.NewWebhookCtx(rec, req, tool.Tool{Key: testKey, Path: "/tools/" + testKey}, cfg))
	return rec
}

// TestWebhookDisabledByDefault is the load-bearing case: an instance that
// nobody configured must not answer, even with a correct signature.
func TestWebhookDisabledByDefault(t *testing.T) {
	body := `{"items":[{"text":"hi","type":"uppercase"}]}`

	// Zero-value config — no toggle row, no secret.
	cfg := stubCfg{}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	rec := httptest.NewRecorder()
	webhookConvert(tool.NewWebhookCtx(rec, req, tool.Tool{Key: testKey}, cfg))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 — an unconfigured instance answered", rec.Code)
	}
}

func TestWebhookDisabledIgnoresValidSignature(t *testing.T) {
	body := `{"items":[{"text":"hi","type":"uppercase"}]}`
	rec := call(body, "s3cr3t", "false", "s3cr3t")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 while disabled", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "HI") {
		t.Fatalf("disabled endpoint did work: %s", rec.Body.String())
	}
}

func TestWebhookConvertsBatch(t *testing.T) {
	body := `{"items":[
		{"text":"hello","type":"uppercase"},
		{"text":"WORLD","type":"lowercase"},
		{"text":"hello world","type":"titlecase"}
	]}`
	rec := call(body, "s3cr3t", "true", "s3cr3t")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q, want application/json", ct)
	}

	var got convertResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %s (%s)", err.Error(), rec.Body.String())
	}
	want := []string{"HELLO", "world", "Hello World"}
	if len(got.Results) != len(want) {
		t.Fatalf("got %d results, want %d", len(got.Results), len(want))
	}
	for i, w := range want {
		if got.Results[i].Result != w {
			t.Errorf("results[%d] = %q, want %q", i, got.Results[i].Result, w)
		}
		if got.Results[i].Error != "" {
			t.Errorf("results[%d] unexpected error %q", i, got.Results[i].Error)
		}
	}
}

// TestWebhookUnknownTypeIsPerItem pins that one bad type does not discard
// the rest of the batch, and does not echo the input back as if it worked.
func TestWebhookUnknownTypeIsPerItem(t *testing.T) {
	body := `{"items":[
		{"text":"hello","type":"uppercase"},
		{"text":"hello","type":"upercase"},
		{"text":"WORLD","type":"lowercase"}
	]}`
	rec := call(body, "s3cr3t", "true", "s3cr3t")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got convertResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %s", err.Error())
	}
	if len(got.Results) != 3 {
		t.Fatalf("got %d results, want 3 — a bad item dropped the batch", len(got.Results))
	}
	if got.Results[1].Error == "" {
		t.Error("misspelled type reported no error")
	}
	if got.Results[1].Result != "" {
		t.Errorf("misspelled type returned a result %q; input must not be echoed as success", got.Results[1].Result)
	}
	if got.Results[0].Result != "HELLO" || got.Results[2].Result != "world" {
		t.Errorf("neighbouring items were affected: %+v", got.Results)
	}
}

func TestWebhookRejects(t *testing.T) {
	valid := `{"items":[{"text":"hi","type":"uppercase"}]}`
	cases := []struct {
		name, body, signWith, enabled, secret string
		want                                  int
	}{
		{
			name: "enabled but no secret configured",
			body: valid, signWith: "", enabled: "true", secret: "",
			want: http.StatusServiceUnavailable,
		},
		{
			name: "missing signature header",
			body: valid, signWith: "", enabled: "true", secret: "s3cr3t",
			want: http.StatusUnauthorized,
		},
		{
			name: "signature computed with the wrong key",
			body: valid, signWith: "wrong", enabled: "true", secret: "s3cr3t",
			want: http.StatusUnauthorized,
		},
		{
			name: "malformed JSON with a valid signature",
			body: `{oops`, signWith: "s3cr3t", enabled: "true", secret: "s3cr3t",
			want: http.StatusBadRequest,
		},
		{
			name: "empty items",
			body: `{"items":[]}`, signWith: "s3cr3t", enabled: "true", secret: "s3cr3t",
			want: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(tc.body, tc.signWith, tc.enabled, tc.secret)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			// Every refusal answers JSON — the caller is a program.
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content-type %q, want application/json", ct)
			}
		})
	}
}

// TestWebhookSignatureCoversBody pins that the MAC is checked against the
// bytes actually received, not merely "some valid signature".
func TestWebhookSignatureCoversBody(t *testing.T) {
	sent := `{"items":[{"text":"hi","type":"uppercase"}]}`
	other := `{"items":[{"text":"bye","type":"uppercase"}]}`

	cfg := stubCfg{
		testKey + "/webhook_enabled": "true",
		testKey + "/webhook_secret":  "s3cr3t",
	}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(sent))
	req.Header.Set(signatureHeader, sign(other, "s3cr3t")) // valid, wrong body
	rec := httptest.NewRecorder()
	webhookConvert(tool.NewWebhookCtx(rec, req, tool.Tool{Key: testKey}, cfg))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 — signature was not bound to the body", rec.Code)
	}
}

// TestWebhookTruncatedSignature pins that a short signature is refused
// rather than matching on a prefix.
func TestWebhookTruncatedSignature(t *testing.T) {
	body := `{"items":[{"text":"hi","type":"uppercase"}]}`
	cfg := stubCfg{
		testKey + "/webhook_enabled": "true",
		testKey + "/webhook_secret":  "s3cr3t",
	}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req.Header.Set(signatureHeader, sign(body, "s3cr3t")[:32])
	rec := httptest.NewRecorder()
	webhookConvert(tool.NewWebhookCtx(rec, req, tool.Tool{Key: testKey}, cfg))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 for a truncated signature", rec.Code)
	}
}

// TestWebhookBatchCap pins the ceiling on work an unauthenticated caller
// can request in one request.
func TestWebhookBatchCap(t *testing.T) {
	items := make([]string, maxBatch+1)
	for i := range items {
		items[i] = `{"text":"hi","type":"uppercase"}`
	}
	body := `{"items":[` + strings.Join(items, ",") + `]}`
	rec := call(body, "s3cr3t", "true", "s3cr3t")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413 for %d items", rec.Code, len(items))
	}
}

// TestWebhookToggleAcceptsCommonTruthyValues pins that CfgBool handles the
// forms a checkbox/toggle widget can store.
func TestWebhookToggleAcceptsCommonTruthyValues(t *testing.T) {
	body := `{"items":[{"text":"hi","type":"uppercase"}]}`
	for _, enabled := range []string{"true", "1", "TRUE"} {
		rec := call(body, "s3cr3t", enabled, "s3cr3t")
		if rec.Code != http.StatusOK {
			t.Errorf("enabled=%q: status %d, want 200", enabled, rec.Code)
		}
	}
	for _, disabled := range []string{"", "false", "0", "off"} {
		rec := call(body, "s3cr3t", disabled, "s3cr3t")
		if rec.Code != http.StatusNotFound {
			t.Errorf("enabled=%q: status %d, want 404", disabled, rec.Code)
		}
	}
}
