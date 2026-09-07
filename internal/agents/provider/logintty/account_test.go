package logintty

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigDirEnvOverrideWins(t *testing.T) {
	got := ConfigDir(provider.TypeClaude, []string{"FOO=1", "CLAUDE_CONFIG_DIR=/x/cfg"})
	if got != "/x/cfg" {
		t.Fatalf("claude dir = %q, want /x/cfg", got)
	}
	got = ConfigDir(provider.TypeCodex, []string{"CODEX_HOME=/y/codex"})
	if got != "/y/codex" {
		t.Fatalf("codex dir = %q, want /y/codex", got)
	}
}

func TestConfigDirDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got, want := ConfigDir(provider.TypeClaude, nil), filepath.Join(home, ".claude"); got != want {
		t.Fatalf("claude dir = %q, want %q", got, want)
	}
	if got, want := ConfigDir(provider.TypeCodex, nil), filepath.Join(home, ".codex"); got != want {
		t.Fatalf("codex dir = %q, want %q", got, want)
	}
	if got, want := ConfigDir(provider.TypeGemini, nil), filepath.Join(home, ".gemini"); got != want {
		t.Fatalf("gemini dir = %q, want %q", got, want)
	}
	if got := ConfigDir(provider.TypeWick, nil); got != "" {
		t.Fatalf("wick dir = %q, want empty", got)
	}
}

func TestReadAccountClaudeFromConfigDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok","expiresAt":1767225600000,"subscriptionType":"max"}}`)
	writeFile(t, filepath.Join(dir, ".claude.json"),
		`{"oauthAccount":{"emailAddress":"dev@abc.com","organizationName":"abc org"}}`)

	acc := ReadAccount(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + dir})
	if !acc.Connected {
		t.Fatal("must be connected")
	}
	if acc.Email != "dev@abc.com" || acc.Plan != "max" || acc.Org != "abc org" {
		t.Fatalf("acc = %+v", acc)
	}
	if acc.AuthMethod != "Claude AI" {
		t.Fatalf("AuthMethod = %q, want Claude AI", acc.AuthMethod)
	}
	if want := time.UnixMilli(1767225600000); !acc.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", acc.ExpiresAt, want)
	}
}

func TestReadAccountClaudeMissingFiles(t *testing.T) {
	acc := ReadAccount(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()})
	if acc.Connected {
		t.Fatalf("empty dir must not be connected: %+v", acc)
	}
}

func codexJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	return b64([]byte(`{"alg":"none"}`)) + "." + b64(payload) + ".sig"
}

func TestReadAccountCodexFromJWT(t *testing.T) {
	dir := t.TempDir()
	jwt := codexJWT(t, map[string]any{
		"email":                       "dev@abc.com",
		"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "plus"},
	})
	authJSON, _ := json.Marshal(map[string]any{"tokens": map[string]any{"id_token": jwt}})
	writeFile(t, filepath.Join(dir, "auth.json"), string(authJSON))

	acc := ReadAccount(provider.TypeCodex, []string{"CODEX_HOME=" + dir})
	if !acc.Connected || acc.Email != "dev@abc.com" || acc.Plan != "plus" {
		t.Fatalf("acc = %+v", acc)
	}
	if acc.AuthMethod != "ChatGPT" {
		t.Fatalf("AuthMethod = %q, want ChatGPT", acc.AuthMethod)
	}
}

func TestReadAccountCodexAPIKeyOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "auth.json"), `{"OPENAI_API_KEY":"sk-x"}`)
	acc := ReadAccount(provider.TypeCodex, []string{"CODEX_HOME=" + dir})
	if !acc.Connected || acc.Plan != "api-key" {
		t.Fatalf("acc = %+v", acc)
	}
	if acc.AuthMethod != "API key" {
		t.Fatalf("AuthMethod = %q, want API key", acc.AuthMethod)
	}
}

func TestReadAccountGeminiActiveAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeFile(t, filepath.Join(home, ".gemini", "google_accounts.json"), `{"active":"dev@gmail.com"}`)
	writeFile(t, filepath.Join(home, ".gemini", "oauth_creds.json"), `{"expiry_date":1767225600000}`)

	acc := ReadAccount(provider.TypeGemini, nil)
	if !acc.Connected || acc.Email != "dev@gmail.com" {
		t.Fatalf("acc = %+v", acc)
	}
	if want := time.UnixMilli(1767225600000); !acc.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", acc.ExpiresAt, want)
	}
}
