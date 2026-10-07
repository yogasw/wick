package logintty

// omp multi-account support.
//
// One omp profile holds a credential POOL: agent.db auth_credentials has
// any number of rows per provider (identity_key dedupes a re-login of the
// same account; another account adds a row), and omp rotates between them
// on usage limits. wick reads the pool through `omp usage --json` — the
// only official machine-readable listing (cli/usage-cli.ts): live
// accounts are `reports` (with usage) + `accountsWithoutUsage`, torn-down
// ones are `disabledCredentials`. Temporary usage-limit blocks
// (auth_credential_blocks) are not in that output, so they are not shown.
//
// Removal: omp has no non-interactive per-account logout (the TUI
// /logout account picker is the only one). `omp auth-broker logout
// <provider>` deletes every local credential of one provider
// (cli/auth-broker-cli.ts runLogout → deleteAuthCredentials), so that is
// what wick offers, labelled as provider-wide.
//
// OAuth providers: `omp auth-broker list --json` prints the compiled-in
// registry ([{id,name}]); wick reads it instead of hardcoding.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/pkg/envscrub"
	"github.com/yogasw/wick/pkg/safeexec"
)

// PoolAccount is one account of an instance: one login to one provider.
// omp: a row of the profile's credential pool (several per provider).
// opencode: one auth.json entry (opencode keeps one per provider).
type PoolAccount struct {
	// ID is stable within one listing (omp: provider#n, opencode: provider).
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
	Email    string `json:"email,omitempty"`
	Plan     string `json:"plan,omitempty"`
	Org      string `json:"org,omitempty"`
	// Kind is "oauth" or "api_key" when omp reports it.
	Kind string `json:"kind,omitempty"`
	// Status is "active" or "disabled".
	Status        string    `json:"status"`
	DisabledCause string    `json:"disabled_cause,omitempty"`
	DisabledAt    time.Time `json:"disabled_at,omitzero"`
	// Usage is the account's own windows (reports only).
	Usage []UsageWindow `json:"usage,omitempty"`
}

// ompOrgAndPlan splits omp's identity metadata into org + plan. omp's
// openai-codex login hook stores the ChatGPT planType AS orgName
// (registry/oauth/openai-codex.ts), so for Codex an orgName equal to the
// planType — or any orgName when planType is absent — is the plan.
func ompOrgAndPlan(prov, orgName, planType string) (org, plan string) {
	org, plan = strings.TrimSpace(orgName), strings.TrimSpace(planType)
	if strings.HasPrefix(prov, "openai-codex") {
		if plan == "" {
			plan, org = org, ""
		} else if org == plan {
			org = ""
		}
	}
	return org, plan
}

// ompPool flattens a usage JSON into pool rows: reports, then accounts
// without usage, then disabled tombstones.
func ompPool(u *ompUsageJSON) []PoolAccount {
	if u == nil {
		return nil
	}
	var out []PoolAccount
	now := time.Now()
	for _, r := range u.Reports {
		email, _ := r.Metadata["email"].(string)
		orgName, _ := r.Metadata["orgName"].(string)
		planType, _ := r.Metadata["planType"].(string)
		org, plan := ompOrgAndPlan(r.Provider, orgName, planType)
		one := &ompUsageJSON{Reports: u.Reports[:0:0]}
		one.Reports = append(one.Reports, r)
		w := ompWindows(one)
		for i := range w {
			w[i].ObservedAt = now
		}
		out = append(out, PoolAccount{Provider: r.Provider, Email: email, Org: org, Plan: plan, Kind: "oauth", Status: "active", Usage: w})
	}
	for _, a := range u.AccountsWithoutUsage {
		org, plan := ompOrgAndPlan(a.Provider, a.OrgName, "")
		out = append(out, PoolAccount{Provider: a.Provider, Email: a.Email, Org: org, Plan: plan, Kind: a.Type, Status: "active"})
	}
	for _, d := range u.DisabledCredentials {
		org, plan := ompOrgAndPlan(d.Provider, d.OrgName, "")
		p := PoolAccount{Provider: d.Provider, Email: d.Email, Org: org, Plan: plan, Kind: d.Type, Status: "disabled", DisabledCause: d.Cause}
		if d.DisabledAtMs > 0 {
			p.DisabledAt = time.UnixMilli(int64(d.DisabledAtMs))
		}
		out = append(out, p)
	}
	return out
}

// labelPool fills ID + Label: provider#n per provider, email when known.
func labelPool(pool []PoolAccount) []PoolAccount {
	n := map[string]int{}
	for i := range pool {
		a := &pool[i]
		n[a.Provider]++
		a.ID = fmt.Sprintf("%s#%d", a.Provider, n[a.Provider])
		a.Label = a.Email
		if a.Label == "" {
			a.Label = fmt.Sprintf("%s account %d", a.Provider, n[a.Provider])
		}
	}
	return pool
}

// ListAccounts reports t's accounts: omp's credential pool, opencode's
// logged-in providers across all its account folders (ID
// "<folder>/<provider>"); nil for other types.
func ListAccounts(t provider.Type, env []string) []PoolAccount {
	switch t {
	case provider.TypeOMP:
		u, err := fetchOMPUsage(env, false)
		if err != nil {
			return nil
		}
		return labelPool(ompPool(u))
	case provider.TypeOpencode:
		return opencodeAllAccounts(env)
	}
	return nil
}

// logoutRunner execs the per-type logout: omp `--profile <p> auth-broker
// logout <provider>`, opencode `auth logout <provider>` (non-interactive
// when the provider is given — cli/cmd/providers.ts). Swapped in tests.
var logoutRunner = func(ctx context.Context, t provider.Type, env []string, prov string) ([]byte, error) {
	var cmd *exec.Cmd
	var release func()
	var err error
	if t == provider.TypeOMP {
		cmd, release, err = ompCommand(ctx, env, "auth-broker", "logout", prov)
	} else {
		cmd, release, err = cliCommand(ctx, env, "opencode", "auth", "logout", prov)
	}
	if err != nil {
		return nil, err
	}
	defer release()
	return cmd.CombinedOutput()
}

// cliCommand resolves the instance binary (AccountBinEnvKey, else def) and
// builds a scrubbed-env command; release must run after it ended.
func cliCommand(ctx context.Context, env []string, def string, args ...string) (*exec.Cmd, func(), error) {
	bin := envValue(env, provider.AccountBinEnvKey)
	if bin == "" {
		bin = def
	}
	resolved, err := safeexec.ResolveBin(bin)
	if err != nil {
		return nil, nil, fmt.Errorf("%s binary not found: %w", def, err)
	}
	// A full omp/opencode process: inside the memory guard like an agent
	// spawn ("omp-usage", "omp-auth-broker", "opencode-auth" …), with the
	// limit of the instance env belongs to. release runs once the
	// process has ended.
	cmd, release := provider.HelperCommand(ctx, provider.InstanceForAccountEnv(env), helperLabel(def, args), resolved, args...)
	cmd.Env = append(envscrub.ScrubOSEnv(), env...)
	return cmd, release, nil
}

// helperLabel names a helper scope: "<cli>-<subcommand>", the first
// argument that is not a flag (or a flag's value) — "omp-usage",
// "omp-auth-broker", "opencode-auth".
func helperLabel(def string, args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--profile" {
			i++
			continue
		}
		if !strings.HasPrefix(a, "-") {
			return def + "-" + a
		}
	}
	return def + "-cli"
}

func ompCommand(ctx context.Context, env []string, args ...string) (*exec.Cmd, func(), error) {
	return cliCommand(ctx, env, "omp", append([]string{"--profile", ompProfileFromEnv(env)}, args...)...)
}

// LogoutProvider removes every stored credential of prov from t's store.
// prov must be a provider the pool currently lists, so argv only ever
// carries an id omp itself reported.
func LogoutProvider(t provider.Type, env []string, prov string) error {
	if t != provider.TypeOMP && t != provider.TypeOpencode {
		return fmt.Errorf("logout is not supported for %s", t)
	}
	known := false
	for _, a := range ListAccounts(t, env) {
		if a.Provider == prov {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("no stored credentials for %q", prov)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := logoutRunner(ctx, t, env, prov)
	if t == provider.TypeOMP {
		invalidateOMPUsage(env)
	}
	if err != nil {
		return fmt.Errorf("%s logout %s: %w: %s", t, prov, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func invalidateOMPUsage(env []string) {
	ompUsageMu.Lock()
	delete(ompUsageCache, ompConfigDir(env))
	ompUsageMu.Unlock()
}

// ompOAuthListRunner execs `omp auth-broker list --json`. Swapped in tests.
var ompOAuthListRunner = func(ctx context.Context, env []string) ([]byte, error) {
	cmd, release, err := ompCommand(ctx, env, "auth-broker", "list", "--json")
	if err != nil {
		return nil, err
	}
	defer release()
	return cmd.Output()
}

// ompTestedLogins are the ids whose flow wick has exercised end to end;
// every other registry entry is offered as beta.
var ompTestedLogins = map[string]bool{"openai-codex-device": true, "openai-codex": true, "anthropic": true}

var (
	ompOAuthMu    sync.Mutex
	ompOAuthCache = map[string]ompOAuthEntry{}
)

type ompOAuthEntry struct {
	at   time.Time
	list []OMPLoginProvider
}

const ompOAuthCacheTTL = 10 * time.Minute

// ompLoginProviders is the picker for env's omp binary: the curated
// OMPLoginProviders first (their labels + warnings), then every other id
// from the binary's registry marked beta. Falls back to the curated list
// when the binary cannot be asked.
func ompLoginProviders(env []string) []OMPLoginProvider {
	key := envValue(env, provider.AccountBinEnvKey)
	ompOAuthMu.Lock()
	if e, ok := ompOAuthCache[key]; ok && time.Since(e.at) < ompOAuthCacheTTL {
		ompOAuthMu.Unlock()
		return e.list
	}
	ompOAuthMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	list := OMPLoginProviders
	if out, err := ompOAuthListRunner(ctx, env); err == nil {
		if merged, ok := mergeOMPOAuthList(out); ok {
			list = merged
		}
	}
	ompOAuthMu.Lock()
	ompOAuthCache[key] = ompOAuthEntry{at: time.Now(), list: list}
	ompOAuthMu.Unlock()
	return list
}

func mergeOMPOAuthList(out []byte) ([]OMPLoginProvider, bool) {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '['); i > 0 {
		s = s[i:]
	}
	var reg []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(s), &reg) != nil || len(reg) == 0 {
		return nil, false
	}
	merged := append([]OMPLoginProvider(nil), OMPLoginProviders...)
	seen := map[string]bool{}
	for _, p := range merged {
		seen[p.ID] = true
	}
	for _, r := range reg {
		if r.ID == "" || seen[r.ID] || !opencodeProviderIDRe.MatchString(r.ID) {
			continue
		}
		seen[r.ID] = true
		label := r.Name
		if label == "" {
			label = r.ID
		}
		p := OMPLoginProvider{ID: r.ID, Label: label}
		if !ompTestedLogins[r.ID] {
			p.Label += " (beta)"
			p.Beta = true
		}
		merged = append(merged, p)
	}
	return merged, true
}
