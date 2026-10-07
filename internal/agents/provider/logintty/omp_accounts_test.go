package logintty

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// ompPoolFixture is a trimmed `omp usage --json`: two Codex accounts in one
// profile (the second without usage) and one disabled Anthropic row. Codex
// carries its plan in orgName, exactly as omp's login hook stores it.
const ompPoolFixture = `{
 "reports":[{"provider":"openai-codex","limits":[{"id":"primary","window":{"id":"5h","durationMs":18000000,"resetsAt":1790000000000},"amount":{"usedFraction":0.25}}],
   "metadata":{"email":"a@example.test","planType":"plus","orgName":"plus"}}],
 "accountsWithoutUsage":[{"provider":"openai-codex","type":"oauth","email":"b@example.test","orgName":"free"}],
 "disabledCredentials":[{"provider":"anthropic","type":"oauth","email":"c@example.test","cause":"invalid_grant","disabledAtMs":1780000000000}]
}`

func stubOMPUsage(t *testing.T, out string) {
	t.Helper()
	prev := ompRunner
	ompRunner = func(context.Context, []string) ([]byte, error) { return []byte(out), nil }
	ompUsageMu.Lock()
	ompUsageCache = map[string]ompUsageEntry{}
	ompUsageMu.Unlock()
	t.Cleanup(func() { ompRunner = prev })
}

func TestOMPOrgAndPlan(t *testing.T) {
	cases := []struct{ prov, org, plan, wantOrg, wantPlan string }{
		{"openai-codex", "free", "", "", "free"},
		{"openai-codex", "plus", "plus", "", "plus"},
		{"openai-codex", "Acme", "team", "Acme", "team"},
		{"anthropic", "Acme", "", "Acme", ""},
	}
	for _, c := range cases {
		org, plan := ompOrgAndPlan(c.prov, c.org, c.plan)
		if org != c.wantOrg || plan != c.wantPlan {
			t.Errorf("%s %q/%q: got org=%q plan=%q", c.prov, c.org, c.plan, org, plan)
		}
	}
}

func TestOMPPoolListsEveryAccount(t *testing.T) {
	stubOMPUsage(t, ompPoolFixture)
	got := ListAccounts(provider.TypeOMP, []string{"OMP_PROFILE=pool-test"})
	if len(got) != 3 {
		t.Fatalf("want 3 pool rows, got %d: %+v", len(got), got)
	}
	if got[0].ID != "openai-codex#1" || got[1].ID != "openai-codex#2" || got[0].Label != "a@example.test" {
		t.Errorf("ids/labels: %+v", got[:2])
	}
	if got[0].Plan != "plus" || got[0].Org != "" || got[0].Status != "active" || len(got[0].Usage) != 1 || got[0].Usage[0].Key != "five_hour" {
		t.Errorf("report row wrong: %+v", got[0])
	}
	if got[1].Email != "b@example.test" || got[1].Plan != "free" || got[1].Kind != "oauth" {
		t.Errorf("no-usage row wrong: %+v", got[1])
	}
	if got[2].Status != "disabled" || got[2].DisabledCause != "invalid_grant" || got[2].DisabledAt.IsZero() {
		t.Errorf("disabled row wrong: %+v", got[2])
	}
	if ListAccounts(provider.TypeClaude, nil) != nil {
		t.Error("non-omp types have no pool")
	}
}

func TestReadOMPAccountPlanNotProvider(t *testing.T) {
	stubOMPUsage(t, ompPoolFixture)
	a := readOMPAccount([]string{"OMP_PROFILE=pool-test"})
	if a.Plan != "plus" || a.AuthMethod != "openai-codex" {
		t.Errorf("plan/auth mapping wrong: %+v", a)
	}
	if !strings.Contains(a.Org, "+1 more accounts") {
		t.Errorf("org should count the second live account (disabled excluded): %q", a.Org)
	}
}

func TestAccountZeroExpiryOmitted(t *testing.T) {
	b, _ := json.Marshal(Account{Connected: true})
	if strings.Contains(string(b), "expires_at") {
		t.Errorf("zero expiry must not be serialised: %s", b)
	}
}

func TestLogoutProviderOnlyKnownProviders(t *testing.T) {
	stubOMPUsage(t, ompPoolFixture)
	var called []string
	prev := logoutRunner
	logoutRunner = func(_ context.Context, _ provider.Type, _ []string, prov string) ([]byte, error) {
		called = append(called, prov)
		return []byte("Logged out"), nil
	}
	t.Cleanup(func() { logoutRunner = prev })
	env := []string{"OMP_PROFILE=pool-test"}
	if err := LogoutProvider(provider.TypeOMP, env, "openai-codex; rm -rf /"); err == nil {
		t.Fatal("unknown provider must be refused before exec")
	}
	if err := LogoutProvider(provider.TypeOMP, env, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	if len(called) != 1 || called[0] != "openai-codex" {
		t.Errorf("runner calls: %v", called)
	}
	if err := LogoutProvider(provider.TypeClaude, env, "x"); err == nil {
		t.Error("claude logout is not supported")
	}
}

func TestMergeOMPOAuthList(t *testing.T) {
	out := []byte(`banner
[{"id":"anthropic","name":"Anthropic (Claude Pro/Max)"},{"id":"github-copilot","name":"GitHub Copilot"},{"id":"bad id!","name":"x"}]`)
	got, ok := mergeOMPOAuthList(out)
	if !ok {
		t.Fatal("parse failed")
	}
	if got[0].ID != OMPLoginProviders[0].ID || len(got) != len(OMPLoginProviders)+1 {
		t.Fatalf("curated first, then new ids only: %+v", got)
	}
	last := got[len(got)-1]
	if last.ID != "github-copilot" || !last.Beta || !strings.HasSuffix(last.Label, "(beta)") {
		t.Errorf("registry-only id should be beta: %+v", last)
	}
	if _, ok := mergeOMPOAuthList([]byte("not json")); ok {
		t.Error("garbage must fall back")
	}
}

func TestOMPLoginCommandAcceptsRegistryID(t *testing.T) {
	prev := ompOAuthListRunner
	ompOAuthListRunner = func(context.Context, []string) ([]byte, error) {
		return []byte(`[{"id":"github-copilot","name":"GitHub Copilot"}]`), nil
	}
	ompOAuthMu.Lock()
	ompOAuthCache = map[string]ompOAuthEntry{}
	ompOAuthMu.Unlock()
	t.Cleanup(func() { ompOAuthListRunner = prev })
	env := []string{"OMP_PROFILE=p1"}
	if got := ompLoginCommand(env, "github-copilot"); got[len(got)-1] != "github-copilot" {
		t.Errorf("registry id should reach argv: %v", got)
	}
	if got := ompLoginCommand(env, "nope"); got[len(got)-1] != OMPLoginProviders[0].ID {
		t.Errorf("unknown id falls back to default: %v", got)
	}
}

func TestAPIKeyProvidersAndSetEnvVar(t *testing.T) {
	env := []string{"A=1", "OPENROUTER_API_KEY=x"}
	var or *APIKeyProvider
	list := APIKeyProviders(provider.Instance{Type: provider.TypeOMP, Env: env})
	for i := range list {
		if list[i].ID == "openrouter" {
			or = &list[i]
		}
	}
	if or == nil || or.Env != "OPENROUTER_API_KEY" || !or.Set {
		t.Fatalf("openrouter row: %+v", or)
	}
	if v, ok := APIKeyEnvVar(provider.Instance{Type: provider.TypeOpencode, Name: "oc"}, "google"); !ok || v != "GOOGLE_GENERATIVE_AI_API_KEY" {
		t.Errorf("opencode google env: %q %v", v, ok)
	}
	if _, ok := APIKeyEnvVar(provider.Instance{Type: provider.TypeClaude}, "openrouter"); ok {
		t.Error("claude has no API-key picker")
	}
	got := SetEnvVar(env, "OPENROUTER_API_KEY", "y")
	if strings.Join(got, ",") != "A=1,OPENROUTER_API_KEY=y" {
		t.Errorf("replace: %v", got)
	}
	got = SetEnvVar(got, "NEW", "z")
	if got[len(got)-1] != "NEW=z" {
		t.Errorf("append: %v", got)
	}
	if got = SetEnvVar(got, "OPENROUTER_API_KEY", ""); strings.Contains(strings.Join(got, ","), "OPENROUTER") {
		t.Errorf("empty removes: %v", got)
	}
}

func TestOpencodeAccountsAndLogout(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Synthetic, non-credential values only.
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"openrouter":{"type":"api","key":"x"},"openai":{"type":"oauth","access":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"XDG_DATA_HOME=" + data}
	got := ListAccounts(provider.TypeOpencode, env)
	if len(got) != 2 || got[0].ID != "main/openai" || got[0].Kind != "oauth" || got[1].ID != "main/openrouter" || got[1].Kind != "api" {
		t.Fatalf("opencode accounts: %+v", got)
	}
	var gotType provider.Type
	var gotProv string
	prev := logoutRunner
	logoutRunner = func(_ context.Context, ty provider.Type, _ []string, prov string) ([]byte, error) {
		gotType, gotProv = ty, prov
		return nil, nil
	}
	t.Cleanup(func() { logoutRunner = prev })
	if err := LogoutProvider(provider.TypeOpencode, env, "anthropic"); err == nil {
		t.Error("provider not in auth.json must be refused")
	}
	if err := LogoutProvider(provider.TypeOpencode, env, "openrouter"); err != nil || gotType != provider.TypeOpencode || gotProv != "openrouter" {
		t.Errorf("logout: err=%v type=%s prov=%s", err, gotType, gotProv)
	}
}
