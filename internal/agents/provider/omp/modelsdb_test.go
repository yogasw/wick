package omp

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// writeDB builds a sqlite file with the given statements (test fixture).
func writeDB(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func agentDBWith(t *testing.T, dir string, providers ...string) string {
	t.Helper()
	p := filepath.Join(dir, "agent.db")
	stmts := []string{`CREATE TABLE auth_credentials (id INTEGER PRIMARY KEY, provider TEXT NOT NULL, credential_type TEXT NOT NULL, data TEXT NOT NULL, disabled_cause TEXT DEFAULT NULL)`}
	for _, pr := range providers {
		stmts = append(stmts, `INSERT INTO auth_credentials (provider, credential_type, data) VALUES ('`+pr+`', 'oauth', 'secret-not-read')`)
	}
	stmts = append(stmts, `INSERT INTO auth_credentials (provider, credential_type, data, disabled_cause) VALUES ('xai', 'api', 'x', 'disabled')`)
	writeDB(t, p, stmts...)
	return p
}

// The file reader applies omp's own listing rule: providers with an enabled
// credential, kind chat, sorted provider/id — and never decodes other rows.
func TestReadModelsDB(t *testing.T) {
	dir := t.TempDir()
	agent := agentDBWith(t, dir, "openai-codex")
	models := filepath.Join(dir, "models.db")
	codex := `[{"id":"gpt-6-luna","name":"GPT 6 Luna","provider":"openai-codex"},{"id":"gpt-5.5","name":"GPT 5.5","provider":"openai-codex"},{"id":"gpt-image-2","provider":"openai-codex","kind":"image"}]`
	writeDB(t, models,
		`CREATE TABLE model_cache (provider_id TEXT PRIMARY KEY, version INTEGER NOT NULL, updated_at INTEGER NOT NULL, authoritative INTEGER NOT NULL DEFAULT 0, models TEXT NOT NULL)`,
		`INSERT INTO model_cache VALUES ('openai-codex:0.159.0', 13, 1, 1, '`+codex+`')`,
		`INSERT INTO model_cache VALUES ('xai', 13, 1, 1, 'not json at all')`, // no credential: never decoded
		`INSERT INTO model_cache VALUES ('anthropic', 13, 1, 1, '[{"id":"claude","provider":"anthropic"}]')`,
	)
	got, err := readModelsDB(context.Background(), models, agent)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if want := []string{"openai-codex/gpt-5.5", "openai-codex/gpt-6-luna"}; !slices.Equal(ids, want) {
		t.Fatalf("ids %v, want %v", ids, want)
	}
	if got[0].Desc != "GPT 5.5" {
		t.Fatalf("desc %q", got[0].Desc)
	}
	// Missing file: an error, never a spawn.
	if _, err := readModelsDB(context.Background(), filepath.Join(dir, "none.db"), agent); err == nil {
		t.Fatal("missing models.db read as empty success")
	}
}

// Parity with the real CLI: a SCRATCH COPY of a profile's models.db (never
// the original) + that profile's `omp models --json` output, both captured
// on the host. MODELSDB_COPY / MODELSDB_CLI_JSON / MODELSDB_PROVIDERS.
func TestModelsDBMatchesCLI(t *testing.T) {
	copyPath, cliJSON := os.Getenv("MODELSDB_COPY"), os.Getenv("MODELSDB_CLI_JSON")
	if copyPath == "" || cliJSON == "" {
		t.Skip("MODELSDB_COPY / MODELSDB_CLI_JSON not set")
	}
	providers := []string{"openai-codex"}
	if p := os.Getenv("MODELSDB_PROVIDERS"); p != "" {
		providers = []string{p}
	}
	agent := agentDBWith(t, t.TempDir(), providers...)
	got, err := readModelsDB(context.Background(), copyPath, agent)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(cliJSON)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Models []struct {
			Selector string `json:"selector"`
			Kind     string `json:"kind"`
		} `json:"models"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var want, have []string
	for _, m := range doc.Models {
		if m.Kind == "" || m.Kind == "chat" {
			want = append(want, m.Selector)
		}
	}
	for _, m := range got {
		have = append(have, m.ID)
	}
	if !slices.Equal(have, want) {
		t.Fatalf("models.db %v\n     CLI %v", have, want)
	}
	t.Logf("models.db == omp models --json: %v", have)
}

// The broker token comes from the profile's token file when it exists: no
// omp process.
func TestBrokerTokenFromFile(t *testing.T) {
	home := t.TempDir()
	prev := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = prev })
	t.Setenv("PI_CONFIG_DIR", "")
	f := brokerTokenFile(home, "", "wick-owner")
	if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("tok-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var spawned []string
	t.Cleanup(provider.ObserveHelpersForTest(func(label string, _ *provider.Instance) { spawned = append(spawned, label) }))
	tok, err := brokerToken(context.Background(), brokerSpec{bin: "/nonexistent/omp", profile: "wick-owner"})
	if err != nil || tok != "tok-from-file" || len(spawned) != 0 {
		t.Fatalf("tok=%q err=%v spawned=%v", tok, err, spawned)
	}
}

// A layout this reader was not written for is never guessed at: unknown
// format → nothing (the UI shows Refresh).
func TestReadModelsDBUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	agent := agentDBWith(t, dir, "openai-codex")
	row := `'[{"id":"gpt-5.5","provider":"openai-codex"}]'`
	cases := map[string][]string{
		"column renamed": {
			`CREATE TABLE model_cache (provider_id TEXT PRIMARY KEY, version INTEGER NOT NULL, payload TEXT NOT NULL)`,
			`INSERT INTO model_cache VALUES ('openai-codex', 13, ` + row + `)`,
		},
		"row version unknown": {
			`CREATE TABLE model_cache (provider_id TEXT PRIMARY KEY, version INTEGER NOT NULL, models TEXT NOT NULL)`,
			`INSERT INTO model_cache VALUES ('openai-codex', 14, ` + row + `)`,
		},
		"models not a list": {
			`CREATE TABLE model_cache (provider_id TEXT PRIMARY KEY, version INTEGER NOT NULL, models TEXT NOT NULL)`,
			`INSERT INTO model_cache VALUES ('openai-codex', 13, '"oops"')`,
		},
	}
	for name, stmts := range cases {
		path := filepath.Join(dir, name+".db")
		writeDB(t, path, stmts...)
		got, err := readModelsDB(context.Background(), path, agent)
		if err != errModelsDBUnknown || got != nil {
			t.Errorf("%s: got %v err %v, want unknown format", name, got, err)
		}
	}
}
