package agents

import (
	"context"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
)

// account_quota.go exposes ONE reading to callers outside this package:
// how much of a provider account's rate-limited quota is spent — the
// "Session (5hr) 25% · Weekly 9%" panel, as data.
//
// It exists because an agent asking "what is my usage" means that panel
// at least as often as it means the token ledger: the ledger says what a
// conversation cost, the quota says whether the next turn will run at
// all. Answering only the first is answering a different question.
//
// It goes through the same paced, per-account cache every other reader
// uses (usage_probe.go), so an agent polling this cannot contribute to a
// rate limit — which is the one thing that would make the answer worse
// by asking for it.

// AccountQuotaWindow is one rate-limit window of an account.
type AccountQuotaWindow struct {
	Key         string
	Utilization float64
	ResetsAt    time.Time
	ObservedAt  time.Time
}

// AccountQuota is the whole reading for one provider instance.
type AccountQuota struct {
	Provider string
	// Supported is false for provider types with no usage API at all
	// (gemini, wick). Reason says so in words a caller can print.
	Supported bool
	Reason    string

	Connected  bool
	Plan       string
	Org        string
	AuthMethod string
	ExpiresAt  time.Time

	Windows []AccountQuotaWindow

	// FetchedAt is when the reading was taken — never omitted, because a
	// cached number passed off as live is how a stale quota gets acted on.
	FetchedAt time.Time
	// Pending: no reading yet, one is queued behind the pacing gate.
	Pending bool
	// Checking: a probe is in flight right now.
	Checking bool
	// NextAt is the earliest a manual re-check would be accepted. The
	// panel shows it as a countdown; a caller that knows it will not ask
	// again before then.
	NextAt time.Time
	// Err is a failed probe (expired token, rate limit), reported as
	// itself rather than as an empty set of windows.
	Err string
}

// ProviderAccountQuota reads the cached quota for a provider key
// ("claude/default"). ok is false when no such provider exists.
//
// Never forces a probe: it serves what the cache knows and lets the
// background refresh do the rest.
func ProviderAccountQuota(ctx context.Context, key string) (AccountQuota, bool) {
	t, name, ok := splitProviderKey(key)
	if !ok {
		return AccountQuota{}, false
	}
	ins, err := provider.Find(t, name)
	if err != nil {
		return AccountQuota{}, false
	}
	out := AccountQuota{Provider: string(ins.Type) + "/" + ins.Name}

	acc := logintty.ReadAccount(ins.Type, ins.Env)
	out.Connected, out.Plan, out.Org = acc.Connected, acc.Plan, acc.Org
	out.AuthMethod, out.ExpiresAt = acc.AuthMethod, acc.ExpiresAt

	if !logintty.SupportsUsage(ins.Type) {
		out.Reason = string(ins.Type) + " does not report usage limits"
		return out, true
	}
	out.Supported = true

	ctx, cancel := context.WithTimeout(ctx, connectionsUsageTimeout)
	defer cancel()
	v := usageProbes.getWait(ctx, logintty.UsageIdentity(ins.Type, ins.Env), func() ([]logintty.UsageWindow, error) {
		return logintty.ReadUsage(ins.Type, ins.Env)
	}, logintty.CredentialsChangedAt(ins.Type, ins.Env))

	out.Checking, out.FetchedAt, out.NextAt = v.Checking, v.FetchedAt, v.NextAt
	switch {
	case v.Err != nil:
		out.Err = v.Err.Error()
	case !v.Known:
		out.Pending = true
	default:
		for _, w := range v.Windows {
			out.Windows = append(out.Windows, AccountQuotaWindow{
				Key: w.Key, Utilization: w.Utilization,
				ResetsAt: w.ResetsAt, ObservedAt: w.ObservedAt,
			})
		}
	}
	return out, true
}
