package logintty

// omp (oh-my-pi) per-type login TTY support.
//
// One instance = one omp profile. The profile arrives in env as
// OMP_PROFILE (provider.AccountEnv) and is ALSO put on the argv, so the
// login writes to exactly the agent.db the spawner reads.
//
// Login (packages/coding-agent/src/cli/login-cli.ts, oauth-terminal.ts):
// `omp login <provider>` prints "Open this URL in your browser:" + the URL,
// then — for paste-code providers — asks "Paste the authorization code (or
// full redirect URL):". openai-codex and anthropic are paste-code providers
// with loopback callbacks on 1455 / 54545 (catalog/src/compat/rules.json),
// which a headless wick host never receives; pasting the redirect URL from
// the browser's address bar into the terminal completes the flow.
// openai-codex-device is the device-code variant (stores as openai-codex),
// the codex --device-auth equivalent and the default here.
//
// Usage + account (cli/usage-cli.ts): `omp usage --json` prints
// {reports:[{provider,limits:[…],metadata:{email,accountId}}],
//  accountsWithoutUsage:[{provider,email,…}], …}. There is no other JSON
// account listing, so the account probe reads the same output, cached.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// OMPLoginProvider is one choice in the UI's login picker.
type OMPLoginProvider struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Warning string `json:"warning,omitempty"`
	Default bool   `json:"default,omitempty"`
	// Beta marks a registry provider whose flow wick has not exercised.
	Beta bool `json:"beta,omitempty"`
}

// OMPLoginProviders are the omp OAuth providers wick offers. Ids are omp's
// own (catalog/src/compat/rules.json /auth/providers).
var OMPLoginProviders = []OMPLoginProvider{
	{ID: "openai-codex-device", Label: "ChatGPT Plus/Pro (Codex) — device code", Default: true},
	{ID: "openai-codex", Label: "ChatGPT Plus/Pro (Codex) — browser, paste redirect URL"},
	{ID: "anthropic", Label: "Claude Pro/Max — browser, paste redirect URL",
		Warning: "Anthropic's terms restrict using a Claude subscription outside Claude's own apps. Using it through omp may violate them and can get the account suspended — prefer the claude provider for Claude subscriptions."},
}

func validOMPLoginProvider(env []string, id string) bool {
	for _, p := range ompLoginProviders(env) {
		if p.ID == id {
			return true
		}
	}
	return false
}

// ompLoginCommand is `--profile <p> login <provider>`. The provider must be
// one the binary's registry lists (ompLoginProviders); anything else falls back to the default rather than reaching argv.
func ompLoginCommand(env []string, loginProvider string) []string {
	if !validOMPLoginProvider(env, loginProvider) {
		loginProvider = OMPLoginProviders[0].ID
	}
	return []string{"--profile", ompProfileFromEnv(env), "login", loginProvider}
}

func ompProfileFromEnv(env []string) string {
	if p := envValue(env, "OMP_PROFILE"); p != "" {
		return p
	}
	return provider.DefaultOMPProfile(string(provider.TypeOMP))
}

// ompConfigDir is the profile's agent dir (utils/src/dirs.ts), where
// agent.db lives.
func ompConfigDir(env []string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := envValue(env, "PI_CONFIG_DIR")
	if root == "" {
		root = ".omp"
	}
	return filepath.Join(home, root, "profiles", ompProfileFromEnv(env), "agent")
}

func ompCredentialsChangedAt(env []string) time.Time {
	st, err := os.Stat(filepath.Join(ompConfigDir(env), "agent.db"))
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// ompUsageJSON is the subset of `omp usage --json` wick reads.
type ompUsageJSON struct {
	Reports []struct {
		Provider string `json:"provider"`
		Limits   []struct {
			ID     string `json:"id"`
			Label  string `json:"label"`
			Window *struct {
				ID         string  `json:"id"`
				DurationMs float64 `json:"durationMs"`
				ResetsAt   float64 `json:"resetsAt"`
			} `json:"window"`
			Amount struct {
				Used              *float64 `json:"used"`
				Limit             *float64 `json:"limit"`
				UsedFraction      *float64 `json:"usedFraction"`
				RemainingFraction *float64 `json:"remainingFraction"`
				Unit              string   `json:"unit"`
			} `json:"amount"`
		} `json:"limits"`
		Metadata map[string]any `json:"metadata"`
	} `json:"reports"`
	AccountsWithoutUsage []struct {
		Provider string `json:"provider"`
		Type     string `json:"type"`
		Email    string `json:"email"`
		OrgName  string `json:"orgName"`
	} `json:"accountsWithoutUsage"`
	DisabledCredentials []struct {
		Provider     string  `json:"provider"`
		Type         string  `json:"type"`
		Email        string  `json:"email"`
		OrgName      string  `json:"orgName"`
		Cause        string  `json:"cause"`
		DisabledAtMs float64 `json:"disabledAtMs"`
	} `json:"disabledCredentials"`
}

// ompRunner execs `omp --profile <p> usage --json`. Swapped in tests.
var ompRunner = func(ctx context.Context, env []string) ([]byte, error) {
	cmd, release, err := ompCommand(ctx, env, "usage", "--json")
	if err != nil {
		return nil, err
	}
	defer release()
	return cmd.Output()
}

// ompUsageCacheTTL bounds how often a FAILED listing is retried (see
// ompUsageCacheValid). The usage windows themselves go through the usage
// probe's own cache/pace gate on top of this.
const ompUsageCacheTTL = 2 * time.Minute

var (
	ompUsageMu    sync.Mutex
	ompUsageCache = map[string]ompUsageEntry{}
)

type ompUsageEntry struct {
	at   time.Time
	data *ompUsageJSON
	err  error
}

func fetchOMPUsage(env []string, fresh bool) (*ompUsageJSON, error) {
	key := ompConfigDir(env)
	ompUsageMu.Lock()
	if e, ok := ompUsageCache[key]; ok && !fresh && ompUsageCacheValid(e, env) {
		ompUsageMu.Unlock()
		return e.data, e.err
	}
	ompUsageMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, err := ompRunner(ctx, env)
	var data *ompUsageJSON
	if err == nil {
		data, err = parseOMPUsage(out)
	}
	ompUsageMu.Lock()
	ompUsageCache[key] = ompUsageEntry{at: time.Now(), data: data, err: err}
	ompUsageMu.Unlock()
	return data, err
}

// ompUsageCacheValid: a failure is retried after ompUsageCacheTTL; a good
// listing is served until agent.db changes (a login, logout or token
// refresh rewrites it) or a usage probe replaces it (fresh). So opening a
// card or the /usage popover never re-execs omp on its own.
func ompUsageCacheValid(e ompUsageEntry, env []string) bool {
	if e.err != nil || e.data == nil {
		return time.Since(e.at) < ompUsageCacheTTL
	}
	return !ompCredentialsChangedAt(env).After(e.at)
}

func parseOMPUsage(out []byte) (*ompUsageJSON, error) {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:] // tolerate a stray banner line before the JSON
	}
	var u ompUsageJSON
	if err := json.Unmarshal([]byte(s), &u); err != nil {
		return nil, fmt.Errorf("omp usage --json: %w", err)
	}
	return &u, nil
}

// readOMPAccount reports the profile's headline account: the first live
// credential of the pool (ompPool). A profile may hold several — omp
// rotates between them — and ListAccounts lists them all; Org then says
// how many more there are.
func readOMPAccount(env []string) Account {
	u, err := fetchOMPUsage(env, false)
	if err != nil || u == nil {
		// A failed `omp usage` is not a logout: omp may be busy, slow to
		// boot or rate-limited, and the next read usually succeeds.
		return Account{Unknown: true}
	}
	var live []PoolAccount
	for _, a := range ompPool(u) {
		if a.Status == "active" {
			live = append(live, a)
		}
	}
	if len(live) == 0 {
		return Account{}
	}
	first := live[0]
	acc := Account{Connected: true, AuthMethod: first.Provider, Email: first.Email, Org: first.Org, Plan: first.Plan}
	if len(live) > 1 {
		acc.Org = strings.TrimSpace(acc.Org + fmt.Sprintf(" (+%d more accounts in this profile)", len(live)-1))
	}
	return acc
}

// readOMPUsage maps omp's limits onto wick's windows, one set per pool
// account, each tagged with the account's ListAccounts ID (Headline gives
// the instance-level view). The 5-hour and 7-day windows get the keys the
// usage rings already know (five_hour / seven_day); anything else keeps
// omp's window id.
func readOMPUsage(env []string) ([]UsageWindow, error) {
	u, err := fetchOMPUsage(env, true)
	if err != nil {
		return nil, err
	}
	var out []UsageWindow
	for _, a := range labelPool(ompPool(u)) {
		for _, w := range a.Usage {
			w.Account = a.ID
			out = append(out, w)
		}
	}
	return out, nil
}

func ompWindows(u *ompUsageJSON) []UsageWindow {
	var out []UsageWindow
	seen := map[string]bool{}
	now := time.Now()
	for _, r := range u.Reports {
		for _, l := range r.Limits {
			frac, ok := ompUsedFraction(l.Amount.UsedFraction, l.Amount.Used, l.Amount.Limit, l.Amount.RemainingFraction, l.Amount.Unit)
			if !ok {
				continue
			}
			key := l.ID
			w := UsageWindow{Utilization: frac * 100, ObservedAt: now}
			if l.Window != nil {
				key = ompWindowKey(l.Window.ID, l.Window.DurationMs)
				if l.Window.ResetsAt > 0 {
					w.ResetsAt = time.UnixMilli(int64(l.Window.ResetsAt))
				}
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			w.Key = key
			out = append(out, w)
		}
	}
	return out
}

// ompUsedFraction mirrors resolveUsedFraction (ai/src/usage.ts): explicit
// fraction > used/limit > percent-unit used > inverted remaining.
func ompUsedFraction(frac, used, limit, remaining *float64, unit string) (float64, bool) {
	switch {
	case frac != nil:
		return *frac, true
	case used != nil && limit != nil && *limit > 0:
		return *used / *limit, true
	case used != nil && unit == "percent":
		return *used / 100, true
	case remaining != nil:
		return 1 - *remaining, true
	}
	return 0, false
}

func ompWindowKey(id string, durationMs float64) string {
	switch {
	case id == "5h" || durationMs == float64(5*time.Hour/time.Millisecond):
		return "five_hour"
	case id == "7d" || durationMs == float64(7*24*time.Hour/time.Millisecond):
		return "seven_day"
	}
	return id
}
