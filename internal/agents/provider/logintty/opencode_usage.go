package logintty

// opencode usage + accounts across its data folders.
//
// opencode has no usage command and `opencode serve` has no usage route,
// so wick reads the one source that exists for its subscription logins:
// ChatGPT's account endpoint `GET https://chatgpt.com/backend-api/wham/usage`
// — the same request the codex CLI's /status and omp's usage provider
// (packages/ai/src/usage/openai-codex.ts) make, with the OAuth bearer and
// ChatGPT-Account-Id opencode already keeps in auth.json. Other providers
// (API keys, Copilot, …) report no usage and are listed without windows.
//
// wick never refreshes an expired token itself: that would rewrite
// auth.json under opencode's feet. An expired login is reported as such
// and opencode renews it on that account's next turn.
//
// Accounts are the instance's data folders (provider.OpencodeAccountIDs:
// "main", a2, …), one row per (folder, logged-in provider), ID
// "<folder>/<provider>".

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// chatgptUsageURL is swapped in tests.
var chatgptUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// opencodeUsageGap spaces the per-account requests of one probe, on top of
// the cache's own pacing between probes.
var opencodeUsageGap = 500 * time.Millisecond

type opencodeAuthEntry struct {
	Type      string `json:"type"`
	Access    string `json:"access"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"accountId"`
}

type opencodeFolder struct {
	id  string // "main", "a2", …
	dir string // <XDG_DATA_HOME of the folder>/opencode
}

// opencodeFolders lists the instance's account folders from its env.
func opencodeFolders(env []string) []opencodeFolder {
	main := opencodeConfigDir(env)
	out := []opencodeFolder{{id: provider.OpencodeMainAccount, dir: main}}
	xdg := envValue(env, "XDG_DATA_HOME")
	if xdg == "" {
		return out
	}
	ins := provider.Instance{Type: provider.TypeOpencode, OpencodeConfig: &provider.OpencodeConfig{DataDir: xdg}}
	root, err := provider.OpencodeAccountsRoot(ins)
	if err != nil {
		return out
	}
	for _, id := range provider.OpencodeAccountIDs(ins)[1:] {
		out = append(out, opencodeFolder{id: id, dir: filepath.Join(root, id, "opencode")})
	}
	return out
}

func readOpencodeAuth(dir string) map[string]opencodeAuthEntry {
	var auth map[string]opencodeAuthEntry
	if dir == "" || !readJSON(filepath.Join(dir, "auth.json"), &auth) {
		return nil
	}
	return auth
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// opencodeHasUsage: only ChatGPT OAuth logins have a usage source.
func opencodeHasUsage(prov string, e opencodeAuthEntry) bool {
	return prov == "openai" && e.Type == "oauth" && e.Access != ""
}

// opencodePlans remembers the ChatGPT plan each account's last probe saw,
// keyed by account dir + provider, for the account rows.
var (
	opencodePlanMu sync.Mutex
	opencodePlans  = map[string]string{}
)

// opencodeAllAccounts lists every (folder, provider) of the instance.
// Email comes from the ChatGPT id/access token claims when present; the
// token itself is never returned.
func opencodeAllAccounts(env []string) []PoolAccount {
	var out []PoolAccount
	for _, f := range opencodeFolders(env) {
		auth := readOpencodeAuth(f.dir)
		for _, prov := range sortedKeys(auth) {
			e := auth[prov]
			a := PoolAccount{
				ID:       f.id + "/" + prov,
				Label:    "Account " + f.id,
				Provider: prov,
				Kind:     e.Type,
				Status:   "active",
			}
			if opencodeHasUsage(prov, e) {
				a.Email = chatgptTokenEmail(e.Access)
			}
			opencodePlanMu.Lock()
			a.Plan = opencodePlans[f.dir+"|"+prov]
			opencodePlanMu.Unlock()
			out = append(out, a)
		}
	}
	return out
}

func chatgptTokenEmail(token string) string {
	claims, ok := decodeJWTClaims(token)
	if !ok {
		return ""
	}
	if prof, ok := claims["https://api.openai.com/profile"].(map[string]any); ok {
		if e, ok := prof["email"].(string); ok {
			return e
		}
	}
	e, _ := claims["email"].(string)
	return e
}

// readOpencodeUsage probes every ChatGPT login of the instance and tags
// each window with its account. One account failing is a per-account
// placeholder, not a failed probe; only when every probed account fails
// does the probe fail (so a 429's Retry-After still reaches the cache).
func readOpencodeUsage(env []string) ([]UsageWindow, error) {
	var out []UsageWindow
	var lastErr error
	probed, ok := 0, 0
	for _, f := range opencodeFolders(env) {
		auth := readOpencodeAuth(f.dir)
		for _, prov := range sortedKeys(auth) {
			e := auth[prov]
			if !opencodeHasUsage(prov, e) {
				continue
			}
			id := f.id + "/" + prov
			if probed > 0 && opencodeUsageGap > 0 {
				time.Sleep(opencodeUsageGap)
			}
			probed++
			ws, plan, err := fetchChatGPTUsage(e)
			if err != nil {
				lastErr = err
				out = append(out, UsageWindow{Account: id, Error: err.Error()})
				continue
			}
			ok++
			if plan != "" {
				opencodePlanMu.Lock()
				opencodePlans[f.dir+"|"+prov] = plan
				opencodePlanMu.Unlock()
			}
			for _, w := range ws {
				w.Account = id
				out = append(out, w)
			}
		}
	}
	if probed > 0 && ok == 0 {
		return nil, lastErr
	}
	return out, nil
}

// chatgptUsagePayload is the subset of /wham/usage wick reads.
type chatgptUsagePayload struct {
	PlanType  string `json:"plan_type"`
	RateLimit *struct {
		Primary   *chatgptUsageWindow `json:"primary_window"`
		Secondary *chatgptUsageWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type chatgptUsageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds float64  `json:"limit_window_seconds"`
	ResetAfterSeconds  float64  `json:"reset_after_seconds"`
	ResetAt            float64  `json:"reset_at"`
}

func fetchChatGPTUsage(e opencodeAuthEntry) ([]UsageWindow, string, error) {
	if e.Expires > 0 && time.UnixMilli(e.Expires).Before(time.Now()) {
		return nil, "", fmt.Errorf("login token expired — opencode renews it on this account's next turn")
	}
	req, err := http.NewRequest(http.MethodGet, chatgptUsageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+e.Access)
	if e.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", e.AccountID)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, "", &RateLimitedError{Status: resp.Status, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("usage endpoint: %s", resp.Status)
	}
	return parseChatGPTUsage(body, time.Now())
}

func parseChatGPTUsage(body []byte, now time.Time) ([]UsageWindow, string, error) {
	var p chatgptUsagePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, "", fmt.Errorf("usage endpoint: %w", err)
	}
	var out []UsageWindow
	if p.RateLimit != nil {
		for _, w := range []*chatgptUsageWindow{p.RateLimit.Primary, p.RateLimit.Secondary} {
			if w == nil || w.UsedPercent == nil {
				continue
			}
			pct := min(max(*w.UsedPercent, 0), 100)
			uw := UsageWindow{Key: chatgptWindowKey(w.LimitWindowSeconds), Utilization: pct}
			switch {
			case w.ResetAt > 1e12:
				uw.ResetsAt = time.UnixMilli(int64(w.ResetAt))
			case w.ResetAt > 0:
				uw.ResetsAt = time.Unix(int64(w.ResetAt), 0)
			case w.ResetAfterSeconds > 0:
				uw.ResetsAt = now.Add(time.Duration(w.ResetAfterSeconds) * time.Second)
			}
			out = append(out, uw)
		}
	}
	return out, strings.TrimSpace(p.PlanType), nil
}

// chatgptWindowKey names a window by its length, reusing the keys the
// usage rings know for 5h / 7d.
func chatgptWindowKey(secs float64) string {
	switch {
	case secs == 5*3600:
		return "five_hour"
	case secs == 7*24*3600:
		return "seven_day"
	case secs <= 0:
		return "window"
	case int64(secs)%86400 == 0:
		return fmt.Sprintf("%dd", int64(secs)/86400)
	case int64(secs)%3600 == 0:
		return fmt.Sprintf("%dh", int64(secs)/3600)
	}
	return fmt.Sprintf("%dm", int64(secs)/60)
}

// AccountHasUsage reports whether a ListAccounts row has a usage source
// wick can read (opencode: a ChatGPT OAuth login). omp reports usage per
// account itself, so every omp row counts.
func AccountHasUsage(t provider.Type, a PoolAccount) bool {
	if t != provider.TypeOpencode {
		return true
	}
	return a.Provider == "openai" && a.Kind == "oauth"
}
