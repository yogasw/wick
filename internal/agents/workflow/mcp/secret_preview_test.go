package mcp

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/state"
)

// previewDecryptor strips the wick_cenc_ prefix — same pattern as engine tests.
type previewDecryptor struct{}

func (previewDecryptor) Decrypt(token string) (string, error) {
	if strings.HasPrefix(token, "wick_cenc_") {
		return token[len("wick_cenc_"):], nil
	}
	return token, nil
}

func newPreviewOps(wf workflow.Workflow, envVals map[string]string) *Ops {
	dir, _ := os.MkdirTemp("", "wick-mcp-test-*")
	svc := &stubEnvService{stubService: stubService{wf: wf}, envVals: envVals}
	eng := &engine.Engine{
		Executors:   map[workflow.NodeType]workflow.Executor{},
		Descriptors: map[workflow.NodeType]engine.NodeDescriptor{},
		Triggers:    engine.NewTriggerRegistry(),
		Decryptor:   previewDecryptor{},
		StateStore:  state.New(config.NewLayout(dir)),
		Now:         func() time.Time { return time.Now().UTC() },
		IDGen:       engine.NewRunID,
	}
	return &Ops{Service: svc, Engine: eng}
}

// stubEnvService extends stubService with env values + LoadDraft.
type stubEnvService struct {
	stubService
	envVals map[string]string
}

func (s *stubEnvService) LoadEnvValues(string) (map[string]string, error) {
	cp := make(map[string]string, len(s.envVals))
	for k, v := range s.envVals {
		cp[k] = v
	}
	return cp, nil
}

func (s *stubEnvService) LoadDraft(string) (workflow.Workflow, error) {
	return s.wf, nil
}

// ── Preview masking tests ────────────────────────────────────────────

// 1. Plain env → rendered verbatim in preview.
func TestTemplatePreview_PlainEnvRendered(t *testing.T) {
	wf := workflow.Workflow{ID: "wf1"}
	ops := newPreviewOps(wf, map[string]string{"BASE_URL": "https://example.com"})
	res, err := ops.TemplateTest(TemplateTestInput{
		WorkflowID: "wf1",
		Template:   "{{.Env.BASE_URL}}/path",
	})
	if err != nil || !res.OK {
		t.Fatalf("expected OK render, err=%v res=%+v", err, res)
	}
	if res.Rendered != "https://example.com/path" {
		t.Errorf("plain env: got %q", res.Rendered)
	}
}

// 2. Schema secret → rendered as •••••••• in preview (never plaintext).
func TestTemplatePreview_SchemaSecretMasked(t *testing.T) {
	wf := workflow.Workflow{
		ID: "wf2",
		Env: []workflow.EnvField{
			{Name: "API_KEY", Widget: "secret"},
		},
	}
	ops := newPreviewOps(wf, map[string]string{
		"API_KEY": "wick_cenc_mysecrettoken",
	})
	res, err := ops.TemplateTest(TemplateTestInput{
		WorkflowID: "wf2",
		Template:   "Bearer {{.Secret.API_KEY}}",
	})
	if err != nil || !res.OK {
		t.Fatalf("expected OK, err=%v res=%+v", err, res)
	}
	if strings.Contains(res.Rendered, "mysecrettoken") {
		t.Errorf("secret leaked in preview: %q", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "••••••••") {
		t.Errorf("mask not applied: %q", res.Rendered)
	}
}

// 3. Free-form encrypted key (secret toggle ON) → goes to .Secret, masked in preview.
func TestTemplatePreview_FreeFormSecretMasked(t *testing.T) {
	wf := workflow.Workflow{ID: "wf3"}
	ops := newPreviewOps(wf, map[string]string{
		"MY_TOKEN": "wick_cenc_plaintexthere",
		"PLAIN":    "not-secret",
	})
	res, err := ops.TemplateTest(TemplateTestInput{
		WorkflowID: "wf3",
		Template:   "{{.Secret.MY_TOKEN}}",
	})
	if err != nil || !res.OK {
		t.Fatalf("expected OK, err=%v res=%+v", err, res)
	}
	if strings.Contains(res.Rendered, "plaintexthere") {
		t.Errorf("free-form secret leaked: %q", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "••••••••") {
		t.Errorf("mask not applied: %q", res.Rendered)
	}
}

// 4. Free-form plain key → accessible via .Env, rendered verbatim.
func TestTemplatePreview_FreeFormPlainRendered(t *testing.T) {
	wf := workflow.Workflow{ID: "wf4"}
	ops := newPreviewOps(wf, map[string]string{
		"SLACK_CHANNEL": "#support",
	})
	res, err := ops.TemplateTest(TemplateTestInput{
		WorkflowID: "wf4",
		Template:   "{{.Env.SLACK_CHANNEL}}",
	})
	if err != nil || !res.OK {
		t.Fatalf("expected OK, err=%v res=%+v", err, res)
	}
	if res.Rendered != "#support" {
		t.Errorf("plain free-form: got %q", res.Rendered)
	}
}

// 5. env_keys + secret_keys populated in result for autocomplete.
func TestTemplatePreview_KeysInResult(t *testing.T) {
	wf := workflow.Workflow{
		ID: "wf5",
		Env: []workflow.EnvField{
			{Name: "SCHEMA_SECRET", Widget: "secret"},
		},
	}
	ops := newPreviewOps(wf, map[string]string{
		"SCHEMA_SECRET": "wick_cenc_tok",
		"PLAIN_KEY":     "value",
		"FREE_SECRET":   "wick_cenc_free",
	})
	res, err := ops.TemplateTest(TemplateTestInput{
		WorkflowID: "wf5",
		Template:   "ok",
	})
	if err != nil || !res.OK {
		t.Fatalf("expected OK, err=%v res=%+v", err, res)
	}
	envKeySet := map[string]bool{}
	for _, k := range res.EnvKeys {
		envKeySet[k] = true
	}
	secretKeySet := map[string]bool{}
	for _, k := range res.SecretKeys {
		secretKeySet[k] = true
	}
	if !envKeySet["PLAIN_KEY"] {
		t.Errorf("PLAIN_KEY should be in env_keys: %v", res.EnvKeys)
	}
	if envKeySet["SCHEMA_SECRET"] {
		t.Errorf("SCHEMA_SECRET should not be in env_keys")
	}
	if !secretKeySet["SCHEMA_SECRET"] {
		t.Errorf("SCHEMA_SECRET should be in secret_keys: %v", res.SecretKeys)
	}
	if !secretKeySet["FREE_SECRET"] {
		t.Errorf("FREE_SECRET (wick_cenc_) should be in secret_keys: %v", res.SecretKeys)
	}
}
