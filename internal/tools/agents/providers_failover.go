package agents

import (
	"errors"
	"net/http"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// FailoverTargetDTO is one step of a failover chain on the wire.
type FailoverTargetDTO struct {
	// Instance is the "type/name" tag; a bare type means its default.
	Instance string `json:"instance"`
	// ModelID is the model pin, flat or "<path>@<model>"; "" = default.
	ModelID string `json:"model_id,omitempty"`
}

// FailoverConfigDTO is an instance's failover config on the wire.
type FailoverConfigDTO struct {
	Enabled        bool                `json:"enabled"`
	Triggers       []string            `json:"triggers"`
	Chain          []FailoverTargetDTO `json:"chain"`
	RecheckMinutes int                 `json:"recheck_minutes"`
	ReturnHome     bool                `json:"return_home"`
}

// FailoverWarningDTO is a cost of one valid target (history left behind
// on another provider type, …). It never blocks a save.
type FailoverWarningDTO struct {
	Instance string `json:"instance"`
	Message  string `json:"message"`
}

// FailoverResponse is the envelope of GET and POST …/failover. Errors on
// a GET are what the stored chain no longer satisfies (a target removed
// or disabled since); on a POST they are why nothing was saved.
type FailoverResponse struct {
	Config   FailoverConfigDTO    `json:"config"`
	Warnings []FailoverWarningDTO `json:"warnings"`
	Errors   []string             `json:"errors"`
	// CanEdit: may the caller POST this config (failover owner permission).
	CanEdit bool `json:"can_edit"`
}

// FailoverSummaryDTO is the provider list's one-line view of a chain.
type FailoverSummaryDTO struct {
	Enabled bool     `json:"enabled"`
	Targets []string `json:"targets"`
}

// listProviderInstancesFn lists every configured instance. A var so tests
// can stand in for the provider config file, like findProviderInstanceFn.
var listProviderInstancesFn = provider.Load

func failoverDTO(c provider.FailoverConfig) FailoverConfigDTO {
	out := FailoverConfigDTO{
		Enabled:        c.Enabled,
		Triggers:       append([]string{}, c.Triggers...),
		Chain:          make([]FailoverTargetDTO, 0, len(c.Chain)),
		RecheckMinutes: c.RecheckMinutes,
		ReturnHome:     c.ReturnHome,
	}
	for _, t := range c.Chain {
		out.Chain = append(out.Chain, FailoverTargetDTO{Instance: t.Instance, ModelID: t.ModelID})
	}
	return out
}

func failoverFromDTO(d FailoverConfigDTO) provider.FailoverConfig {
	out := provider.FailoverConfig{
		Enabled:        d.Enabled,
		Triggers:       d.Triggers,
		RecheckMinutes: d.RecheckMinutes,
		ReturnHome:     d.ReturnHome,
	}
	for _, t := range d.Chain {
		out.Chain = append(out.Chain, provider.FailoverTarget{Instance: t.Instance, ModelID: t.ModelID})
	}
	return out
}

// failoverSummaryDTO is nil for an instance that never saved a chain.
func failoverSummaryDTO(ins provider.Instance) *FailoverSummaryDTO {
	if ins.Failover == nil {
		return nil
	}
	out := &FailoverSummaryDTO{Enabled: ins.Failover.Enabled, Targets: make([]string, 0, len(ins.Failover.Chain))}
	for _, t := range ins.Failover.Chain {
		out.Targets = append(out.Targets, t.Instance)
	}
	return out
}

// failoverReport fills warnings + errors for cfg as the chain of ins.
func failoverReport(ins provider.Instance, cfg provider.FailoverConfig, all []provider.Instance, extra []string) FailoverResponse {
	resp := FailoverResponse{Config: failoverDTO(cfg), Warnings: []FailoverWarningDTO{}, Errors: []string{}}
	for _, w := range provider.FailoverWarnings(ins, cfg, all) {
		resp.Warnings = append(resp.Warnings, FailoverWarningDTO{Instance: w.Instance, Message: w.Message})
	}
	seen := map[string]bool{}
	for _, e := range append(joinedErrors(provider.ValidateFailover(ins, cfg, all)), extra...) {
		if !seen[e] {
			seen[e] = true
			resp.Errors = append(resp.Errors, e)
		}
	}
	return resp
}

// joinedErrors flattens an errors.Join into its messages.
func joinedErrors(err error) []string {
	if err == nil {
		return nil
	}
	var j interface{ Unwrap() []error }
	if errors.As(err, &j) {
		out := make([]string, 0, len(j.Unwrap()))
		for _, e := range j.Unwrap() {
			out = append(out, e.Error())
		}
		return out
	}
	return []string{err.Error()}
}

// providerFailover returns one instance's failover config (the defaults
// when it never saved one), with per-target warnings and whatever the
// stored chain no longer satisfies. Readable by whoever manages the
// instance, like the rest of its detail page.
// GET /providers/detail/{type}/{name}/failover
func providerFailover(c *tool.Ctx) {
	if !requireApprovedUser(c) {
		return
	}
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	ins, err := findProviderInstanceFn(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return
	}
	if !requireProviderManage(c, t, name) {
		return
	}
	all, err := listProviderInstancesFn()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := failoverReport(ins, provider.EffectiveFailover(ins), all, nil)
	resp.CanEdit = canProviderDo(c, t, name, ownerPermFailover)
	c.JSON(http.StatusOK, resp)
}

// saveProviderFailover replaces one instance's failover config. The body
// is the whole FailoverConfigDTO. Admins, and the instance's owner while
// the failover owner permission allows it, may save.
//
// A target the chain did not hold before must be an instance the editor
// may pick themselves: without that, an owner could chain their instance
// onto an account they were never given. Targets already stored (an
// admin's choice) may stay while that instance still exists — one deleted
// and re-created under the same name is a new instance and is checked.
// A refused target reads exactly like an unknown instance, one error and
// nothing more (not even "disabled"): the existence of an instance is
// itself information.
//
// Invalid → 400 with the errors, nothing stored. Warnings never block.
// POST /providers/detail/{type}/{name}/failover
//
// No agents-manager check: this only edits the provider config file.
func saveProviderFailover(c *tool.Ctx) {
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	if !requireProviderDo(c, t, name, ownerPermFailover) {
		return
	}
	var req FailoverConfigDTO
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	ins, err := findProviderInstanceFn(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return
	}
	all, err := listProviderInstancesFn()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	cfg := provider.NormalizeFailover(failoverFromDTO(req))

	stored := map[string]bool{}
	for _, tg := range provider.EffectiveFailover(ins).Chain {
		st, sn := provider.SplitInstanceTag(tg.Instance)
		stored[provider.InstanceTag(st, sn)] = true
	}
	u := login.GetUser(c.Context())
	denied := map[string]bool{}
	var deniedErrs []string
	for _, tg := range cfg.Chain {
		tt, tn := provider.SplitInstanceTag(tg.Instance)
		if stored[tg.Instance] && instanceListed(all, tt, tn) {
			continue
		}
		if !userCanAccessProvider(c.Context(), u, tt, tn) {
			denied[tg.Instance] = true
			deniedErrs = append(deniedErrs, "unknown provider instance "+tg.Instance)
		}
	}
	// Validate and warn as if the refused targets did not exist, so no
	// other message (disabled, shared login, model) tells them apart.
	visible := make([]provider.Instance, 0, len(all))
	for _, i := range all {
		if !denied[provider.InstanceTag(i.Type, i.Name)] {
			visible = append(visible, i)
		}
	}

	resp := failoverReport(ins, cfg, visible, deniedErrs)
	resp.CanEdit = true
	if len(resp.Errors) > 0 {
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	ins.Failover = &cfg
	if err := saveProviderInstanceFn(ins); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// instanceListed reports whether t/name is among all.
func instanceListed(all []provider.Instance, t provider.Type, name string) bool {
	for _, i := range all {
		if i.Type == t && i.Name == name {
			return true
		}
	}
	return false
}
