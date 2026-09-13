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
//     it is left. Re-check stays manage-only and the reply says which.

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
	})

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
