package logintty

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

// modelsets.go is the omp/opencode provider.ModelSets: Instance → Provider →
// (Account) → Model. Providers are the prefixes of the live CLI model ids
// ("openai-codex/gpt-5.5" → "openai-codex"), i.e. what this login can
// actually run. The account level appears ONLY when a provider has more than
// one active account: "Auto (rotation)" plus one row per account; with one
// account the provider drills straight to its models. A trivial level (one
// provider) is skipped the same way, so the picker never shows a single row
// to click through.
//
// Pins: "<provider>@<model>" or "<provider>/<account>@<model>", account = the
// omp pool index (omp's `/session pin <n>`), never the email.

func init() {
	provider.RegisterModelSets(provider.TypeOMP, cliModelSets{})
	provider.RegisterModelSets(provider.TypeOpencode, cliModelSets{})
	provider.AccountPlanLabel = accountPlanLabel
}

// accountPlanLabel names the plan of the account a refused turn ran on, for
// "gpt-5.5 tidak tersedia untuk akun ChatGPT free ini". omp only: opencode's
// auth.json carries no plan. account "" = the provider's single active
// account (Auto with several is ambiguous → "").
func accountPlanLabel(ins provider.Instance, prov, account string) string {
	if ins.Type != provider.TypeOMP || prov == "" {
		return ""
	}
	accts := providerAccounts(ins)[prov]
	var pick *PoolAccount
	for i := range accts {
		if account != "" && accountSegment(accts[i]) == account {
			pick = &accts[i]
		}
	}
	if account == "" && len(accts) == 1 {
		pick = &accts[0]
	}
	if pick == nil || pick.Plan == "" {
		return ""
	}
	if strings.HasPrefix(prov, "openai-codex") {
		return "ChatGPT " + pick.Plan
	}
	return pick.Plan
}

// Seams for tests: the CLI model list and the account pool.
var (
	modelSetsModels = func(ctx context.Context, ins provider.Instance) ([]provider.ModelSeed, error) {
		models, _, err := provider.CachedCLIModels(ctx, ins, false)
		return models, err
	}
	modelSetsAccounts = func(ins provider.Instance) []PoolAccount {
		if ins.Type == provider.TypeOpencode {
			return opencodeFolderAccounts(ins)
		}
		return ListAccounts(ins.Type, provider.AccountEnv(ins))
	}
)

type cliModelSets struct{}

// modelProvider is the provider prefix of a CLI model id, "" when it has none.
func modelProvider(id string) string {
	if i := strings.IndexByte(id, '/'); i > 0 {
		return id[:i]
	}
	return ""
}

// liveModels is the instance's effective live list (provider.
// EffectiveLiveModels: filter, chosen Default first, refusals marked, the
// default a spawn runs flagged) — the same list the provider page shows.
// nil when live models are off (the flat curated list applies instead).
func liveModels(ctx context.Context, ins provider.Instance) ([]provider.LiveModel, error) {
	if !provider.LiveModelsEnabled(ins) || !ins.ModelSelect {
		return nil, nil
	}
	models, err := modelSetsModels(ctx, ins)
	if len(models) == 0 {
		return nil, err
	}
	return provider.EffectiveLiveModels(ins, models), nil
}

// providerAccounts returns the active accounts per provider, pool order.
func providerAccounts(ins provider.Instance) map[string][]PoolAccount {
	out := map[string][]PoolAccount{}
	for _, a := range modelSetsAccounts(ins) {
		if a.Status == "disabled" {
			continue
		}
		out[a.Provider] = append(out[a.Provider], a)
	}
	return out
}

// accountSegment is the pin segment for a pool row: omp's per-provider index.
func accountSegment(a PoolAccount) string {
	if i := strings.LastIndexByte(a.ID, '#'); i >= 0 {
		return a.ID[i+1:]
	}
	return a.ID
}

func (s cliModelSets) Sets(ctx context.Context, ins provider.Instance) ([]provider.ModelChoice, error) {
	models, err := liveModels(ctx, ins)
	if len(models) == 0 {
		return nil, err
	}
	var order []string
	count := map[string]int{}
	defProv := ""
	for _, m := range models {
		p := modelProvider(m.ID)
		if count[p] == 0 {
			order = append(order, p)
		}
		count[p]++
		if m.Default {
			defProv = p
		}
	}
	if defProv == "" {
		defProv = order[0]
	}
	if len(order) == 1 {
		return s.Expand(ctx, ins, []string{order[0]})
	}
	accts := providerAccounts(ins)
	out := make([]provider.ModelChoice, 0, len(order))
	for _, p := range order {
		label := p
		if p == "" {
			label = "Other"
		}
		desc := fmt.Sprintf("%d models", count[p])
		if n := len(accts[p]); n > 1 {
			desc += fmt.Sprintf(" · %d accounts", n)
		}
		out = append(out, provider.ModelChoice{ID: p, Label: label, Desc: desc, Live: true, Default: p == defProv})
	}
	return out, nil
}

func (cliModelSets) Expand(ctx context.Context, ins provider.Instance, path []string) ([]provider.ModelChoice, error) {
	if len(path) == 0 || len(path) > 2 {
		return nil, nil
	}
	prov := path[0]
	accts := providerAccounts(ins)[prov]
	if len(path) == 1 && len(accts) > 1 {
		out := []provider.ModelChoice{{ID: provider.AutoAccount, Label: "Auto (rotation)", Desc: autoDesc(ins.Type), Live: true, Default: true}}
		for _, a := range accts {
			out = append(out, provider.ModelChoice{ID: accountSegment(a), Label: a.Label, Desc: a.Plan, Live: true})
		}
		return out, nil
	}
	account := ""
	if len(path) == 2 {
		account = path[1]
	}
	models, err := liveModels(ctx, ins)
	if err != nil && len(models) == 0 {
		return nil, err
	}
	pinned := account != "" && account != provider.AutoAccount
	var rows []provider.ModelChoice
	hasDefault := false
	for _, m := range models {
		if modelProvider(m.ID) != prov {
			continue
		}
		label := strings.TrimPrefix(m.ID, prov+"/")
		if pinned {
			// One account's level stands on its own: the Auto view's
			// refusals and default are not this account's. Its own
			// marks come from ApplyAvailability below; the instance's
			// configured default stays the pin it falls back to.
			rows = append(rows, provider.ModelChoice{ID: m.ID, Label: label, Desc: m.Desc,
				Default: m.Default || (m.ID == ins.LiveModelDefault && ins.LiveModelDefault != "")})
			continue
		}
		desc := m.Desc
		if m.Unavailable && desc == "" {
			desc = provider.NotAvailableDesc
			if m.Reason != "" {
				desc += " — " + m.Reason
			}
		}
		rows = append(rows, provider.ModelChoice{ID: m.ID, Label: label, Desc: desc, Default: m.Default, Unavailable: m.Unavailable})
		hasDefault = hasDefault || m.Default
	}
	if pinned {
		// One pinned account: its own refusals and last-worked model.
		return provider.ApplyAvailability(ins, provider.AvailabilityAccount(prov, account), rows), nil
	}
	if !hasDefault {
		// The instance default is under another provider: this level's
		// default is its first usable row.
		for i := range rows {
			if !rows[i].Unavailable {
				rows[i].Default = true
				break
			}
		}
	}
	return rows, nil
}

// autoDesc explains who rotates: omp does it natively; for opencode wick
// moves to the next account on a quota error.
func autoDesc(t provider.Type) string {
	if t == provider.TypeOMP {
		return "omp picks and rotates accounts"
	}
	return "wick rotates on quota errors"
}

func (cliModelSets) Resolve(_ provider.Instance, path []string, model string) (provider.SpawnPin, error) {
	if len(path) == 0 || len(path) > 2 {
		return provider.SpawnPin{}, fmt.Errorf("model pin: want provider[/account], got %d segments", len(path))
	}
	prov := path[0]
	if model == "" || (prov != "" && !strings.HasPrefix(model, prov+"/")) {
		return provider.SpawnPin{}, fmt.Errorf("model pin: %q is not a %s model", model, prov)
	}
	sp := provider.SpawnPin{Model: model, Provider: prov}
	if len(path) == 2 && path[1] != provider.AutoAccount {
		sp.Account = path[1]
	}
	return sp, nil
}

// opencodeFolderAccounts lists opencode's accounts across its data folders:
// one row per (folder, logged-in provider), ID = the folder id ("main",
// "a2", …) so a pin names the folder the spawn runs in.
func opencodeFolderAccounts(ins provider.Instance) []PoolAccount {
	var out []PoolAccount
	for _, id := range provider.OpencodeAccountIDs(ins) {
		for _, p := range provider.OpencodeAuthProviders(ins, id) {
			out = append(out, PoolAccount{ID: id, Label: "Account " + id, Provider: p, Status: "active"})
		}
	}
	return out
}
