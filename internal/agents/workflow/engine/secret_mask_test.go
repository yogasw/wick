package engine

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/state"
)

// fakeDecryptor simulates DecryptMaster — strips the wick_cenc_ prefix
// so tests are deterministic without a real key.
type fakeDecryptor struct{}

func (fakeDecryptor) Decrypt(token string) (string, error) {
	if strings.HasPrefix(token, "wick_cenc_") {
		return token[len("wick_cenc_"):], nil
	}
	if strings.HasPrefix(token, "wick_enc_") {
		return token[len("wick_enc_"):], nil
	}
	return token, nil
}

type countingDecryptor struct{ calls *int }

func (c *countingDecryptor) Decrypt(token string) (string, error) {
	*c.calls++
	s := strings.TrimPrefix(strings.TrimPrefix(token, "wick_cenc_"), "wick_enc_")
	return s, nil
}

// newMaskEngine builds a minimal Engine with fakeDecryptor + temp state store.
func newMaskEngine() *Engine {
	dir, _ := os.MkdirTemp("", "wick-engine-test-*")
	return &Engine{
		Executors:   map[workflow.NodeType]workflow.Executor{},
		Descriptors: map[workflow.NodeType]NodeDescriptor{},
		Triggers:    NewTriggerRegistry(),
		Decryptor:   fakeDecryptor{},
		StateStore:  state.New(config.NewLayout(dir)),
		Now:         func() time.Time { return time.Now().UTC() },
		IDGen:       NewRunID,
	}
}

// ── ExtractSecrets ──────────────────────────────────────────────────

func TestExtractSecrets_SchemaSecret(t *testing.T) {
	eng := newMaskEngine()
	schema := []workflow.EnvField{
		{Name: "API_KEY", Widget: "secret"},
		{Name: "DEBUG", Widget: "text"},
	}
	vals := map[string]string{
		"API_KEY": "wick_cenc_mysecret",
		"DEBUG":   "true",
	}
	got := eng.ExtractSecrets(schema, vals)
	if got["API_KEY"] != "mysecret" {
		t.Errorf("API_KEY: got %q want %q", got["API_KEY"], "mysecret")
	}
	if _, ok := got["DEBUG"]; ok {
		t.Error("DEBUG should not be in secrets")
	}
}

func TestExtractSecrets_FreeFormToken(t *testing.T) {
	eng := newMaskEngine()
	vals := map[string]string{
		"TOKEN": "wick_cenc_tok123",
		"PLAIN": "hello",
	}
	got := eng.ExtractSecrets(nil, vals)
	if got["TOKEN"] != "tok123" {
		t.Errorf("TOKEN: got %q want %q", got["TOKEN"], "tok123")
	}
	if _, ok := got["PLAIN"]; ok {
		t.Error("PLAIN should not be in secrets")
	}
}

func TestExtractSecrets_DecryptCache(t *testing.T) {
	calls := 0
	eng := newMaskEngine()
	eng.Decryptor = &countingDecryptor{calls: &calls}
	vals := map[string]string{
		"A": "wick_cenc_shared",
		"B": "wick_cenc_shared", // same token — should only decrypt once
	}
	eng.ExtractSecrets(nil, vals)
	if calls != 1 {
		t.Errorf("decrypt called %d times, want 1 (cache dedup)", calls)
	}
}

func TestExtractSecrets_NoDecryptor(t *testing.T) {
	eng := newMaskEngine()
	eng.Decryptor = nil
	vals := map[string]string{"K": "wick_cenc_abc"}
	got := eng.ExtractSecrets(nil, vals)
	// No decryptor → token passes through unchanged.
	if got["K"] != "wick_cenc_abc" {
		t.Errorf("without decryptor token should pass through, got %q", got["K"])
	}
}

// ── maskAny ─────────────────────────────────────────────────────────

func TestMaskAny_String(t *testing.T) {
	got := maskAny("url=supersecret&foo=bar", map[string]string{"K": "supersecret"})
	if got != "url=••••••••&foo=bar" {
		t.Errorf("got %q", got)
	}
}

func TestMaskAny_Map(t *testing.T) {
	out := maskAny(map[string]any{
		"body":   "value=tok123",
		"status": float64(200),
	}, map[string]string{"K": "tok123"}).(map[string]any)
	if out["body"] != "value=••••••••" {
		t.Errorf("body: got %q", out["body"])
	}
	if out["status"] != float64(200) {
		t.Error("status should be unchanged")
	}
}

func TestMaskAny_Nested(t *testing.T) {
	out := maskAny(map[string]any{
		"headers": map[string]any{"Authorization": "Bearer pass"},
		"items":   []any{"pass", "safe"},
	}, map[string]string{"K": "pass"}).(map[string]any)

	hdrs := out["headers"].(map[string]any)
	if hdrs["Authorization"] != "Bearer ••••••••" {
		t.Errorf("header: got %q", hdrs["Authorization"])
	}
	items := out["items"].([]any)
	if items[0] != "••••••••" {
		t.Errorf("items[0]: got %q", items[0])
	}
	if items[1] != "safe" {
		t.Error("items[1] should be unchanged")
	}
}

func TestMaskAny_ShortValueSkipped(t *testing.T) {
	// < 4 chars → skip to avoid false positives on "ok", "1" etc.
	got := maskAny("status=ok", map[string]string{"K": "ok"})
	if got != "status=ok" {
		t.Errorf("short secret should not mask, got %q", got)
	}
}

func TestMaskAny_EmptySecrets(t *testing.T) {
	got := maskAny("hello", nil)
	if got != "hello" {
		t.Errorf("no secrets = no change, got %q", got)
	}
}

func TestMaskAny_MultipleSecrets(t *testing.T) {
	got, _ := maskAny("alpha_secret and beta_secret", map[string]string{
		"A": "alpha_secret",
		"B": "beta_secret",
	}).(string)
	if strings.Contains(got, "alpha_secret") || strings.Contains(got, "beta_secret") {
		t.Errorf("plaintext leaked: %q", got)
	}
	if !strings.Contains(got, "••••••••") {
		t.Errorf("mask not applied: %q", got)
	}
}

// ── recordSuccess masking ────────────────────────────────────────────
//
// Secret values must be masked in emitted events (SSE/state) while
// rc.Outputs retains plaintext for downstream node inputs.

func TestRecordSuccess_MasksSecretsInEvent(t *testing.T) {
	eng := newMaskEngine()

	var emittedData map[string]any
	eng.OnEvent = func(_, _ string, ev workflow.RunEvent) {
		if ev.Event == workflow.EventNodeCompleted {
			emittedData = ev.Data
		}
	}

	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{ID: "wf-mask"},
		RunID:      "run-1",
		Secrets:    map[string]string{"API_KEY": "supersecret"},
		Outputs:    map[string]any{},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	st := &workflow.RunState{Outputs: map[string]any{}}
	n := workflow.Node{ID: "step1", Type: workflow.NodeHTTP}
	out := workflow.NodeOutput{
		Fields: map[string]any{
			"body":   "token=supersecret",
			"status": float64(200),
		},
	}

	eng.recordSuccess(context.Background(), "wf-mask", st, rc, n, out, 10)

	if emittedData == nil {
		t.Fatal("no event emitted")
	}
	evOut, _ := emittedData["output"].(map[string]any)
	body, _ := evOut["body"].(string)
	if strings.Contains(body, "supersecret") {
		t.Errorf("secret leaked in emitted event: %q", body)
	}
	if !strings.Contains(body, "••••••••") {
		t.Errorf("mask not applied in emitted event: %q", body)
	}

	// rc.Outputs must keep plaintext for downstream node inputs.
	rawOut, _ := rc.Outputs["step1"].(map[string]any)
	if !strings.Contains(rawOut["body"].(string), "supersecret") {
		t.Errorf("rc.Outputs must keep plaintext for downstream, got %q", rawOut["body"])
	}
}

func TestRecordSuccess_NoSecretsNoMask(t *testing.T) {
	eng := newMaskEngine()
	var emittedData map[string]any
	eng.OnEvent = func(_, _ string, ev workflow.RunEvent) {
		if ev.Event == workflow.EventNodeCompleted {
			emittedData = ev.Data
		}
	}

	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{ID: "wf-plain"},
		RunID:      "run-2",
		Secrets:    map[string]string{},
		Outputs:    map[string]any{},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	st := &workflow.RunState{Outputs: map[string]any{}}
	n := workflow.Node{ID: "n1", Type: workflow.NodeHTTP}
	out := workflow.NodeOutput{Fields: map[string]any{"body": "hello"}}

	eng.recordSuccess(context.Background(), "wf-plain", st, rc, n, out, 5)

	evOut, _ := emittedData["output"].(map[string]any)
	if evOut["body"] != "hello" {
		t.Errorf("no-secret run should not mask: %q", evOut["body"])
	}
}
