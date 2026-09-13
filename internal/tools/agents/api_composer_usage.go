package agents

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
	"github.com/yogasw/wick/pkg/tool"
)

// The `/usage` command: how much of this session's provider account is
// spent, answered inside the conversation instead of sending the user to
// the Providers page — which most of them cannot open anyway (that menu
// is for provider managers).
//
// Two deliberate choices:
//
//   - It reads the SAME paced cache as everything else (usage_probe.go),
//     so opening it is free and cannot contribute to a rate limit. The
//     reply carries the reading's age, and the popover shows it, because
//     a number with no provenance implies a live call nobody made.
//   - It is gated on the ACCESS grant, not manage: if you are allowed to
//     run a session on this provider, you are allowed to see how much of
//     it is left — and to ask for a fresh reading, which the cache is
//     free to refuse. It refuses inside a server-sent Retry-After and
//     within 10s of the last probe, so the button cannot be turned into
//     a rate limit however many people press it.

// composerUsageAccount is the "who is logged in" half of the reply.
type composerUsageAccount struct {
	Connected  bool   `json:"connected"`
	Email      string `json:"email,omitempty"`
	Plan       string `json:"plan,omitempty"`
	Org        string `json:"org,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// ComposerUsageResponse is GET /api/composer/usage.
type ComposerUsageResponse struct {
	Provider string `json:"provider"`
	// Supported is false for provider types with no usage API at all
	// (codex, gemini, wick today). Reason says so in words the popover
	// can print verbatim.
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`

	Account *composerUsageAccount `json:"account,omitempty"`
	Windows []usageWindowDTO      `json:"windows,omitempty"`

	// Err is a failed probe (expired token, rate limit) — shown as text,
	// never as an empty set of bars.
	Err string `json:"error,omitempty"`
	// Pending: no reading yet, one is queued behind the pacing gate.
	Pending bool `json:"pending,omitempty"`
	// Checking: a probe is in flight right now.
	Checking bool `json:"checking,omitempty"`

	FetchedAt string `json:"fetched_at,omitempty"`
	AgeS      int    `json:"age_s,omitempty"`
	NextS     int    `json:"next_s,omitempty"`

	// CanManage tells the popover whether to offer Re-check.
	CanManage bool `json:"can_manage"`
}

// apiComposerUsage handles GET /api/composer/usage?provider=<type>/<name>.
func apiComposerUsage(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	t, name, ok := splitProviderKey(c.Query("provider"))
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider is required, as type/name"})
		return
	}
	ins, err := provider.Find(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return
	}
	// The pick grant, not the manage grant: a provider you may run a
	// session on is one whose remaining quota you may read.
	if !requireProviderAccess(c, ins.Type, ins.Name) {
		return
	}

	res := ComposerUsageResponse{
		Provider:  string(ins.Type) + "/" + ins.Name,
		CanManage: canManageProvider(c, ins.Type, ins.Name),
	}
	acc := logintty.ReadAccount(ins.Type, ins.Env)
	res.Account = &composerUsageAccount{
		Connected:  acc.Connected,
		Email:      acc.Email,
		Plan:       acc.Plan,
		Org:        acc.Org,
		AuthMethod: acc.AuthMethod,
	}
	if !acc.ExpiresAt.IsZero() {
		res.Account.ExpiresAt = acc.ExpiresAt.UTC().Format(time.RFC3339)
	}

	if !logintty.SupportsUsage(ins.Type) {
		res.Reason = string(ins.Type) + " does not report usage limits"
		c.JSON(http.StatusOK, res)
		return
	}
	res.Supported = true

	ctx, cancel := context.WithTimeout(c.Context(), connectionsUsageTimeout)
	defer cancel()
	v := usageProbes.getWait(ctx, logintty.UsageIdentity(ins.Type, ins.Env), func() ([]logintty.UsageWindow, error) {
		return logintty.ReadUsage(ins.Type, ins.Env)
	}, logintty.CredentialsChangedAt(ins.Type, ins.Env))

	now := time.Now()
	res.Checking = v.Checking
	if !v.FetchedAt.IsZero() {
		res.FetchedAt = v.FetchedAt.UTC().Format(time.RFC3339)
		res.AgeS = int(v.Age(now).Round(time.Second) / time.Second)
	}
	if !v.NextAt.IsZero() {
		if d := v.NextAt.Sub(now); d > 0 {
			res.NextS = int(d.Round(time.Second) / time.Second)
		}
	}
	switch {
	case v.Err != nil:
		res.Err = v.Err.Error()
	case !v.Known:
		res.Pending = true
	default:
		res.Windows = usageWindowDTOs(v.Windows)
	}
	c.JSON(http.StatusOK, res)
}

// splitProviderKey parses "type/name", and accepts a bare "type" for the
// instance whose name matches its type (the common single-instance case).
func splitProviderKey(key string) (provider.Type, string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	typ, name, found := strings.Cut(key, "/")
	if !found || name == "" {
		name = typ
	}
	return provider.Type(typ), name, true
}

// ComposerUsageRefreshResponse is POST /api/composer/usage/refresh.
//
// accepted=false is not an error: the cache declined because a probe now
// would land inside a cooldown (its own floor, or one the upstream asked
// for). WaitS says how long, so the popover explains the wait instead of
// looking broken — and so nobody learns to click it repeatedly.
type ComposerUsageRefreshResponse struct {
	Accepted bool `json:"accepted"`
	Checking bool `json:"checking"`
	WaitS    int  `json:"wait_s,omitempty"`
	// Supported=false for a provider type with no usage API — there is
	// nothing to re-check.
	Supported bool `json:"supported"`
}

// apiComposerUsageRefresh handles POST /api/composer/usage/refresh.
//
// Same access gate as the read: this asks the cache for a fresh reading,
// it does not act on the account. The cache owns the decision about
// whether a probe actually goes out.
func apiComposerUsageRefresh(c *tool.Ctx) {
	if notReady(c) || !requireApprovedUser(c) {
		return
	}
	t, name, ok := splitProviderKey(c.Query("provider"))
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider is required, as type/name"})
		return
	}
	ins, err := provider.Find(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return
	}
	if !requireProviderAccess(c, ins.Type, ins.Name) {
		return
	}
	if !logintty.SupportsUsage(ins.Type) {
		c.JSON(http.StatusOK, ComposerUsageRefreshResponse{Supported: false})
		return
	}
	accepted, wait := usageProbes.forceRefresh(logintty.UsageIdentity(ins.Type, ins.Env), func() ([]logintty.UsageWindow, error) {
		return logintty.ReadUsage(ins.Type, ins.Env)
	})
	res := ComposerUsageRefreshResponse{Supported: true, Accepted: accepted, Checking: accepted}
	if !accepted {
		res.WaitS = int(wait.Round(time.Second) / time.Second)
	}
	c.JSON(http.StatusOK, res)
}
