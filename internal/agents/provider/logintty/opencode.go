package logintty

// opencode per-type login TTY support.
//
// One instance = one XDG_DATA_HOME (provider.AccountEnv); opencode keeps
// its credentials in $XDG_DATA_HOME/opencode/auth.json
// (packages/opencode/src/auth/index.ts, core/src/global.ts).
//
// Login: `opencode auth login -p <provider> [-m <method label>]`
// (src/cli/cmd/providers.ts; "auth" is an alias of "providers"). For
// OpenAI the headless method "ChatGPT Pro/Plus (headless)"
// (src/plugin/openai/codex.ts) is a device-code flow, so it works on a
// host with no browser — the codex --device-auth equivalent.
//
// Account probe: `auth list` only prints human text, so the probe reads
// auth.json itself — a plain JSON file, read-only, never written.
// Usage: opencode has no usage command — SupportsUsage stays false.
//
// Claude Pro/Max subscriptions are NOT offered: opencode's docs rule them
// out, so the picker only exposes providers opencode supports for
// subscriptions/keys.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// OpencodeLoginProvider is one choice in the UI's login picker.
type OpencodeLoginProvider struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"-"`
	Method   string `json:"-"`
	Default  bool   `json:"default,omitempty"`
}

// OpencodeLoginProviders are the fallback login choices, used while no
// server catalog is at hand (opencodeLoginChoices). Method labels are
// opencode's own (matched case-insensitively by providers.ts). The first
// is the default, the last ("pick") always closes the picker.
var OpencodeLoginProviders = []OpencodeLoginProvider{
	{ID: "openai-headless", Label: "ChatGPT Plus/Pro — device code", Provider: "openai", Method: "ChatGPT Pro/Plus (headless)", Default: true},
	{ID: "github-copilot", Label: "GitHub Copilot — device code", Provider: "github-copilot"},
	{ID: "pick", Label: "Other provider (pick in the terminal)"},
}

// OpencodeClaudeNote is shown next to the opencode login picker.
const OpencodeClaudeNote = "Claude Pro/Max subscriptions are not supported by opencode; use the claude provider for those."

var opencodeProviderIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// opencodeLoginChoices is the picker for ins: every OAuth method opencode's
// own server lists (GET /provider/auth, names from GET /provider — see
// provider.OpencodeCatalog), then "pick". Without a catalog (server not
// reachable yet, fetch failed) the static OpencodeLoginProviders answer.
// ChatGPT device code stays the default when opencode offers it.
func opencodeLoginChoices(ins provider.Instance) []OpencodeLoginProvider {
	cat := provider.PeekOpencodeCatalog(ins)
	if cat == nil || len(cat.Auth) == 0 {
		return OpencodeLoginProviders
	}
	var out []OpencodeLoginProvider
	for id, methods := range cat.Auth {
		// Claude subscriptions: see OpencodeClaudeNote.
		if id == "anthropic" || !opencodeProviderIDRe.MatchString(id) {
			continue
		}
		name := id
		if p, ok := cat.Provider(id); ok && p.Name != "" {
			name = p.Name
		}
		for i, m := range methods {
			if m.Type != "oauth" || m.Label == "" {
				continue
			}
			c := OpencodeLoginProvider{
				ID:       opencodeMethodChoiceID(id, i),
				Label:    name + " — " + m.Label,
				Provider: id,
				Method:   m.Label,
			}
			if d := OpencodeLoginProviders[0]; id == d.Provider && strings.EqualFold(m.Label, d.Method) {
				c.ID, c.Default = d.ID, true
			}
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return OpencodeLoginProviders
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
	})
	return append(out, OpencodeLoginProviders[len(OpencodeLoginProviders)-1])
}

// opencodeMethodChoiceID is the picker id of provider's method i.
func opencodeMethodChoiceID(provider string, i int) string {
	return provider + "#" + strconv.Itoa(i)
}

// opencodeLoginCommand is `auth login -p <provider> [-m <method>]`. An
// id that is neither a known choice nor a plain provider id falls back to
// the default. "pick" leaves the choice to opencode's own prompt.
func opencodeLoginCommand(ins provider.Instance, choice string) []string {
	if choice == "pick" {
		return []string{"auth", "login"}
	}
	for _, c := range append(opencodeLoginChoices(ins), OpencodeLoginProviders...) {
		if c.ID == choice && c.Provider != "" {
			args := []string{"auth", "login", "-p", c.Provider}
			if c.Method != "" {
				args = append(args, "-m", c.Method)
			}
			return args
		}
	}
	// A catalog id whose catalog is gone (cache reset): the provider alone,
	// opencode then asks for the method.
	if p, _, ok := strings.Cut(choice, "#"); ok {
		choice = p
	}
	if opencodeProviderIDRe.MatchString(choice) && choice != "anthropic" {
		return []string{"auth", "login", "-p", choice}
	}
	d := OpencodeLoginProviders[0]
	return []string{"auth", "login", "-p", d.Provider, "-m", d.Method}
}

// opencodeConfigDir is <XDG_DATA_HOME>/opencode for the instance.
func opencodeConfigDir(env []string) string {
	if d := envValue(env, "XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode")
}

func opencodeCredentialsChangedAt(env []string) time.Time {
	st, err := os.Stat(filepath.Join(opencodeConfigDir(env), "auth.json"))
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// readOpencodeAccount lists the providers auth.json holds credentials for.
// opencode stores no email, so the card shows provider + method.
func readOpencodeAccount(dir string) Account {
	var auth map[string]struct {
		Type      string `json:"type"`
		Expires   int64  `json:"expires"`
		AccountID string `json:"accountId"`
	}
	if !readJSON(filepath.Join(dir, "auth.json"), &auth) || len(auth) == 0 {
		return Account{}
	}
	names := make([]string, 0, len(auth))
	for k := range auth {
		names = append(names, k)
	}
	sort.Strings(names)
	acc := Account{Connected: true, Plan: strings.Join(names, ", ")}
	first := auth[names[0]]
	switch first.Type {
	case "oauth":
		acc.AuthMethod = "OAuth"
		if first.Expires > 0 {
			acc.ExpiresAt = time.UnixMilli(first.Expires)
		}
		acc.Org = first.AccountID
	case "api":
		acc.AuthMethod = "API key"
	default:
		acc.AuthMethod = first.Type
	}
	return acc
}
