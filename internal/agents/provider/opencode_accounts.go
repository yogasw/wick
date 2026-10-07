package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// opencode_accounts.go gives opencode more than one account per provider.
// opencode's auth.json holds ONE credential per provider, so a second login
// to the same provider lives in its own data folder:
// <instance data dir>/accounts/<id> (its own XDG_DATA_HOME/XDG_CONFIG_HOME).
// The instance's own data dir is the account "main". An account is used by
// deriving an Instance whose DataDir is that folder — spawn env, server key
// (hash of env → one `opencode serve` per folder), login TTY and model list
// all follow from OpencodeDataDir, so nothing else needs to know.
//
// Rotation (Auto): a quota / usage-limit / 429 error marks the account
// exhausted for a while; the next turn goes to the next folder logged in to
// the same provider. omp rotates natively and is not handled here.

// OpencodeMainAccount is the instance's own data dir as an account id.
const OpencodeMainAccount = "main"

var opencodeAccountIDRe = regexp.MustCompile(`^a[0-9]{1,4}$`)

// OpencodeAccountsRoot is where extra account folders live.
func OpencodeAccountsRoot(ins Instance) (string, error) {
	dir, err := OpencodeDataDir(ins)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "accounts"), nil
}

// OpencodeAccountIDs lists the account folders: "main" first, then the
// extra folders in creation order (a2, a3, …). A sharer lists its owner's:
// WithOpencodeAccount still derives the sharer's own folder, which spawn
// links to the owner's (EnsureOpencodeAuthLink).
func OpencodeAccountIDs(ins Instance) []string {
	ins = authOwnerOrSelf(ins)
	out := []string{OpencodeMainAccount}
	root, err := OpencodeAccountsRoot(ins)
	if err != nil {
		return out
	}
	ents, _ := os.ReadDir(root)
	var extra []string
	for _, e := range ents {
		if e.IsDir() && opencodeAccountIDRe.MatchString(e.Name()) {
			extra = append(extra, e.Name())
		}
	}
	sort.Slice(extra, func(i, j int) bool { return accountNum(extra[i]) < accountNum(extra[j]) })
	return append(out, extra...)
}

func accountNum(id string) int {
	n := 0
	fmt.Sscanf(strings.TrimPrefix(id, "a"), "%d", &n)
	return n
}

// WithOpencodeAccount is ins pointed at account id's data folder. "" and
// "main" return ins unchanged.
func WithOpencodeAccount(ins Instance, id string) (Instance, error) {
	if id == "" || id == OpencodeMainAccount {
		return ins, nil
	}
	if !opencodeAccountIDRe.MatchString(id) {
		return ins, fmt.Errorf("opencode: bad account id %q", id)
	}
	root, err := OpencodeAccountsRoot(ins)
	if err != nil {
		return ins, err
	}
	// Only the data folder changes: the instance's other opencode settings
	// (hosted allow-list, default model) apply to every account.
	cfg := OpencodeConfig{}
	if ins.OpencodeConfig != nil {
		cfg = *ins.OpencodeConfig
	}
	cfg.DataDir = filepath.Join(root, id)
	cp := ins
	cp.OpencodeConfig = &cfg
	return cp, nil
}

// NewOpencodeAccount creates the next account folder (0700) and returns
// its id and the derived instance to log in with.
func NewOpencodeAccount(ins Instance) (string, Instance, error) {
	if owner, ok := AuthOwner(ins); ok {
		return "", ins, fmt.Errorf("opencode instance %s uses the login of %s — add the account there", ins.Name, owner.Name)
	}
	root, err := OpencodeAccountsRoot(ins)
	if err != nil {
		return "", ins, err
	}
	next := 2
	for _, id := range OpencodeAccountIDs(ins)[1:] {
		if n := accountNum(id); n >= next {
			next = n + 1
		}
	}
	id := fmt.Sprintf("a%d", next)
	if err := os.MkdirAll(filepath.Join(root, id), 0o700); err != nil {
		return "", ins, err
	}
	derived, err := WithOpencodeAccount(ins, id)
	return id, derived, err
}

// OpencodeAuthProviders lists the provider ids logged in to one account
// folder (auth.json keys only; credential values are never decoded).
func OpencodeAuthProviders(ins Instance, id string) []string {
	acc, err := WithOpencodeAccount(authOwnerOrSelf(ins), id)
	if err != nil {
		return nil
	}
	path, err := OpencodeAuthFile(acc)
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var auth map[string]json.RawMessage
	if json.Unmarshal(b, &auth) != nil {
		return nil
	}
	out := make([]string, 0, len(auth))
	for k := range auth {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// OpencodeProviderAccounts are the account ids logged in to prov, in order.
func OpencodeProviderAccounts(ins Instance, prov string) []string {
	var out []string
	for _, id := range OpencodeAccountIDs(ins) {
		for _, p := range OpencodeAuthProviders(ins, id) {
			if p == prov {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

// accountExhaustedFor is how long a quota-hit account is skipped by Auto.
const accountExhaustedFor = 30 * time.Minute

var (
	exhaustedMu  sync.Mutex
	exhaustedAt  = map[string]time.Time{}
	exhaustedNow = time.Now
)

// exhaustedKey is per login: a sharer marks and skips its owner's accounts.
func exhaustedKey(ins Instance, prov, account string) string {
	ins = authOwnerOrSelf(ins)
	return string(ins.Type) + "/" + ins.Name + "/" + prov + "/" + account
}

// MarkAccountExhausted records a quota / usage-limit hit on account.
func MarkAccountExhausted(ins Instance, prov, account string) {
	exhaustedMu.Lock()
	exhaustedAt[exhaustedKey(ins, prov, account)] = exhaustedNow()
	exhaustedMu.Unlock()
}

// PickRotatingAccount is the account Auto uses for prov: the first one not
// exhausted within accountExhaustedFor; when all are, the one exhausted
// longest ago. "" when accounts is empty.
func PickRotatingAccount(ins Instance, prov string, accounts []string) string {
	if len(accounts) == 0 {
		return ""
	}
	exhaustedMu.Lock()
	defer exhaustedMu.Unlock()
	now := exhaustedNow()
	oldest, oldestAt := accounts[0], now.Add(time.Hour)
	for _, a := range accounts {
		at, hit := exhaustedAt[exhaustedKey(ins, prov, a)]
		if !hit || now.Sub(at) >= accountExhaustedFor {
			return a
		}
		if at.Before(oldestAt) {
			oldest, oldestAt = a, at
		}
	}
	return oldest
}

// OpencodeAutoAccount is the folder an Auto turn on prov runs in.
func OpencodeAutoAccount(ins Instance, prov string) string {
	return PickRotatingAccount(ins, prov, OpencodeProviderAccounts(ins, prov))
}

// OpencodeSpawnAccount resolves which account folder a spawn runs in: the
// pinned account, else Auto rotation over the folders logged in to the
// pin's provider. "" = the instance's own folder.
func OpencodeSpawnAccount(ins Instance, pin string) (prov, account string) {
	if p, ok := ResolvePin(&ins, pin); ok {
		prov, account = p.Provider, p.Account
	} else if pin != "" && !isForeignModelPin(pin) {
		prov = modelPrefix(pin)
	}
	if account == AutoAccount {
		account = ""
	}
	if account == "" && prov != "" {
		account = OpencodeAutoAccount(ins, prov)
	}
	if account == OpencodeMainAccount {
		account = ""
	}
	return prov, account
}

// IsQuotaError reports a usage-limit / quota / rate-limit turn error.
func IsQuotaError(msg string) bool {
	low := strings.ToLower(msg)
	for _, s := range []string{"429", "rate limit", "rate_limit", "usage limit", "usage_limit", "quota", "too many requests"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}
