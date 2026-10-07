package provider

import "maps"

// livemodels.go is THE effective live model list of an omp/opencode
// instance: the CLI's list, narrowed by the saved filter, the chosen
// Default first, each row marked when the account was refused that model,
// and the effective default picked by the same rule a spawn uses
// (pickLiveDefault: the chosen Default unless refused, else the model that
// last worked, else the first not refused). The provider page
// (GET …/cli-models) and the composer picker (ModelSets) both read it, so
// they can never disagree about what is offered or which model runs.

// LiveModel is one row of an instance's live model list.
type LiveModel struct {
	ID   string
	Desc string
	// Unavailable: listed by the CLI, but this account was refused it
	// (model_not_found / no access). It never becomes the default.
	Unavailable bool
	// Reason is the recorded refusal reason ("" when none was given).
	Reason string
	// Default marks the row a spawn with no pin runs.
	Default bool
}

// NotAvailableDesc is the row text for a refused model.
const NotAvailableDesc = "Not available for this account"

// refusals is every model refused for ins on the Auto key of its provider
// prefix (what a turn with no account pin records), model → reason.
func refusals(ins Instance) map[string]string {
	modelStateMu.Lock()
	defer modelStateMu.Unlock()
	st, _ := loadModelState(ins)
	out := map[string]string{}
	for acct, e := range st.Accounts {
		if acct == "" {
			continue
		}
		for m, r := range e.Unavailable {
			if modelPrefix(m) == acct {
				out[m] = r
			}
		}
	}
	return out
}

// MarkLiveModels marks the refused rows of models (any list, filtered or
// not), order preserved, no default chosen.
func MarkLiveModels(ins Instance, models []ModelSeed) []LiveModel {
	bad := refusals(ins)
	out := make([]LiveModel, 0, len(models))
	for _, m := range models {
		row := LiveModel{ID: m.ID, Desc: m.Desc}
		if r, ok := bad[m.ID]; ok {
			row.Unavailable, row.Reason = true, r
		}
		out = append(out, row)
	}
	return out
}

// EffectiveLiveModels is the list the instance offers from the CLI list
// all: saved filter, chosen Default first, refusals marked, the effective
// default flagged. Empty when nothing matches.
func EffectiveLiveModels(ins Instance, all []ModelSeed) []LiveModel {
	offered := LiveDefaultFirst(FilterLiveModels(ins, all), ins.LiveModelDefault)
	rows := MarkLiveModels(ins, offered)
	def := pickLiveDefault(ins, offered)
	for i := range rows {
		rows[i].Default = rows[i].ID == def && !rows[i].Unavailable
	}
	return rows
}

// ClearModelRefusal forgets that ins was refused model, on every account
// key: the operator's explicit "re-check". The next turn on it tries
// again, and a second refusal records it again.
func ClearModelRefusal(ins Instance, model string) bool {
	modelStateMu.Lock()
	defer modelStateMu.Unlock()
	st, path := loadModelState(ins)
	changed := false
	for _, e := range st.Accounts {
		if _, ok := e.Unavailable[model]; ok {
			m := maps.Clone(e.Unavailable)
			delete(m, model)
			e.Unavailable, changed = m, true
		}
	}
	if changed {
		saveModelState(st, path)
	}
	return changed
}
