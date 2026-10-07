package provider

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// modelwatch.go feeds modelavail.go from real turns: the agent's stdout
// reader shows every line of a grouped-picker CLI (omp/opencode) to a
// modelTurnWatch, which marks the model refused on an access error and
// "last worked" when a turn ends clean. wick's own engine is not watched —
// it resolves and reports models itself.

var (
	reModelField    = regexp.MustCompile(`"model(?:ID)?"\s*:\s*"([^"]+)"`)
	reProviderField = regexp.MustCompile(`"provider(?:ID)?"\s*:\s*"([^"]+)"`)
	rePlanField     = regexp.MustCompile(`"plan_?[tT]ype"\s*:\s*"([^"]+)"`)
)

type modelTurnWatch struct {
	ins    Instance
	model  string // "" until a pin or a stream line names it
	key    string
	failed bool
	// opencode rotation: the provider and account folder this turn runs in
	// (quotaAcct "main" = the instance's own folder); pinned = the user
	// chose that account, so wick never rotates away from it.
	quotaProv, quotaAcct string
	pinned               bool
	// notice writes a system line into the session transcript; nil = none.
	notice func(string)
	// rotate re-runs this turn once on the next account (Auto only);
	// returns false when the turn was already a retry.
	rotate  func() bool
	noticed bool

	// mu guards what the reader loop reads from another goroutine (see
	// refusal): the model and whether this turn was refused it, with the
	// plan the refusal named.
	mu      sync.Mutex
	refused bool
	plan    string
}

// AccountPlanLabel names the plan of a provider account for messages
// ("ChatGPT free"); set by the logintty package, which owns the account
// listing. account "" = the provider's only/active account. "" = unknown.
var AccountPlanLabel = func(ins Instance, prov, account string) string { return "" }

// isErrorLine: only an error frame may count as a quota hit — a model
// answer that merely mentions "429" must not rotate accounts.
func isErrorLine(line string) bool {
	return strings.Contains(line, `"type":"error"`) || strings.Contains(line, `"stopReason":"error"`)
}

// newModelTurnWatch returns nil when ins is not a watched type.
func newModelTurnWatch(ins *Instance, pin string) *modelTurnWatch {
	if ins == nil || ins.Type == TypeWick {
		return nil
	}
	if _, ok := ModelSetsFor(ins.Type); !ok {
		return nil
	}
	w := &modelTurnWatch{ins: *ins}
	if p, ok := ResolvePin(ins, pin); ok {
		w.model, w.key = p.Model, AvailabilityAccount(p.Provider, p.Account)
	} else if pin != "" && !isForeignModelPin(pin) {
		w.model, w.key = pin, modelPrefix(pin)
	}
	if ins.Type == TypeOpencode {
		prov, acct := OpencodeSpawnAccount(*ins, pin)
		if acct == "" {
			acct = OpencodeMainAccount
		}
		w.quotaProv, w.quotaAcct = prov, acct
		if p, ok := ResolvePin(ins, pin); ok && p.Account != "" && p.Account != AutoAccount {
			w.pinned = true
		}
	}
	return w
}

func modelPrefix(id string) string {
	if i := strings.IndexByte(id, '/'); i > 0 {
		return id[:i]
	}
	return ""
}

// seedModelFromArgv fills the model from the spawn's --model when no pin
// named it.
func (w *modelTurnWatch) seedModelFromArgv(argv []string) {
	if w == nil || w.model != "" {
		return
	}
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--model" || argv[i] == "-m" {
			if m := strings.TrimSpace(argv[i+1]); m != "" && !isForeignModelPin(m) {
				w.mu.Lock()
				w.model, w.key = m, modelPrefix(m)
				w.mu.Unlock()
			}
			return
		}
	}
}

// learnModel fills the model from the stream when no pin named it (the CLI
// ran its own default): omp's message "provider"+"model", opencode's
// "providerID"+"modelID".
func (w *modelTurnWatch) learnModel(line string) {
	if w.model != "" {
		return
	}
	m := reModelField.FindStringSubmatch(line)
	if m == nil {
		return
	}
	model := m[1]
	if !strings.Contains(model, "/") {
		if p := reProviderField.FindStringSubmatch(line); p != nil {
			model = p[1] + "/" + model
		}
	}
	w.mu.Lock()
	w.model, w.key = model, modelPrefix(model)
	w.mu.Unlock()
}

// takeRefusal reports the model this turn was refused and its availability
// key, once: a second call (another Error event of the same turn, or the
// reader's exit) sees ok=false, so a refusal is acted on a single time.
func (w *modelTurnWatch) takeRefusal() (model, key string, ok bool) {
	if w == nil {
		return "", "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	ok = w.refused && w.model != ""
	w.refused = false
	return w.model, w.key, ok
}

// announceRefusal posts the "not available — pick another model" line.
// The plan lookup may exec the CLI (omp usage, cached): never on the line
// reader's goroutine.
func (w *modelTurnWatch) announceRefusal() {
	w.mu.Lock()
	ins, model, plan, say := w.ins, w.model, w.plan, w.say
	w.mu.Unlock()
	prov, acct := modelPrefix(model), w.account()
	go func() {
		if label := AccountPlanLabel(ins, prov, acct); label != "" {
			plan = label
		}
		say(ModelUnavailableMessage(model, plan))
	}()
}

// account is the pool account the turn ran on, "" when Auto/unknown.
func (w *modelTurnWatch) account() string {
	if i := strings.IndexByte(w.key, '#'); i >= 0 {
		return w.key[i+1:]
	}
	return ""
}

func (w *modelTurnWatch) say(msg string) {
	if w.notice != nil && msg != "" {
		w.notice(msg)
	}
}

// rotationNotice handles a quota hit on an opencode account: Auto moves to
// the provider's next account and re-runs this turn there once; a pinned
// account or the last one left just says so.
func (w *modelTurnWatch) rotationNotice() string {
	from := w.quotaAcct
	if w.pinned {
		return fmt.Sprintf("Akun %s (%s) kena limit — akun ini dipilih manual, jadi tidak dipindah. Pilih akun lain atau Auto.", from, w.quotaProv)
	}
	next := OpencodeAutoAccount(w.ins, w.quotaProv)
	if next == "" {
		next = OpencodeMainAccount
	}
	if next == from {
		return fmt.Sprintf("Akun %s (%s) kena limit dan tidak ada akun lain yang tersedia untuk provider ini.", from, w.quotaProv)
	}
	if w.rotate != nil && w.rotate() {
		return fmt.Sprintf("Akun %s (%s) kena limit → pindah ke akun %s; pesan ini diulang sekali di sana.", from, w.quotaProv, next)
	}
	return fmt.Sprintf("Akun %s (%s) kena limit → turn berikutnya memakai akun %s.", from, w.quotaProv, next)
}

// turnEnded reports a line that closes one turn of the CLI's JSON stream.
func turnEnded(t Type, line string) bool {
	switch t {
	case TypeOMP:
		return strings.Contains(line, `"type":"agent_end"`)
	case TypeOpencode:
		return strings.Contains(line, `"type":"step_finish"`)
	}
	return false
}

func (w *modelTurnWatch) observe(line string) {
	if w == nil {
		return
	}
	w.learnModel(line)
	if IsModelAccessError(line) {
		w.failed = true
		if w.model != "" {
			reason, plan := "", ""
			if p := rePlanField.FindStringSubmatch(line); p != nil {
				reason, plan = "plan "+p[1], p[1]
			}
			MarkModelUnavailable(w.ins, w.key, w.model, reason)
			// The account's list may have changed: re-read it (no spawn).
			HarvestCLIModels(w.ins)
			if !w.noticed {
				w.noticed = true
				w.mu.Lock()
				w.refused, w.plan = true, plan
				w.mu.Unlock()
				// With auto-retry on, the reader decides what to say: a
				// retry note, or this same line when no model is left.
				if !autoRetryModelOn(&w.ins) {
					w.announceRefusal()
				}
			}
		}
		return
	}
	if w.ins.Type == TypeOpencode && w.quotaProv != "" && isErrorLine(line) && IsQuotaError(line) {
		w.failed = true
		if w.noticed {
			return
		}
		w.noticed = true
		MarkAccountExhausted(w.ins, w.quotaProv, w.quotaAcct)
		w.say(w.rotationNotice())
		return
	}
	if turnEnded(w.ins.Type, line) {
		// A finished turn is when the CLI may have refreshed its own model
		// cache: harvest it (files / running server, never a spawn).
		HarvestCLIModels(w.ins)
		w.noticed = false
		if !w.failed && w.model != "" {
			MarkModelWorked(w.ins, w.key, w.model)
			if w.key != "" {
				MarkModelWorked(w.ins, "", w.model) // instance-wide default
			}
		}
		w.failed = false
	}
}

// pickLiveDefault applies the availability rule to an ordered live list
// (operator pin already first): the pin unless refused, else the model that
// last worked on this instance, else the first not refused. "" when the
// list is empty.
func pickLiveDefault(ins Instance, list []ModelSeed) string {
	if len(list) == 0 {
		return ""
	}
	refused := func(id string) bool {
		_, bad := ModelUnavailable(ins, modelPrefix(id), id)
		return bad
	}
	if pin := strings.TrimSpace(ins.LiveModelDefault); pin != "" && list[0].ID == pin && !refused(pin) {
		return pin
	}
	if last := LastWorkedModel(ins, ""); last != "" && !refused(last) {
		for _, m := range list {
			if m.ID == last {
				return last
			}
		}
	}
	for _, m := range list {
		if !refused(m.ID) {
			return m.ID
		}
	}
	return list[0].ID
}

// ProvenDefaultModel is the default for a spawn with no pin, but ONLY when
// wick has evidence (a model that worked or one that was refused): without
// it the CLI's own default stands, unchanged. Peeks the cached live list,
// never execs.
func ProvenDefaultModel(ins Instance) string {
	if !LiveModelsEnabled(ins) || (LastWorkedModel(ins, "") == "" && !anyRefusal(ins)) {
		return ""
	}
	return pickLiveDefault(ins, LiveDefaultFirst(FilterLiveModels(ins, PeekCLIModels(ins)), ins.LiveModelDefault))
}

func anyRefusal(ins Instance) bool {
	modelStateMu.Lock()
	defer modelStateMu.Unlock()
	st, _ := loadModelState(ins)
	for _, e := range st.Accounts {
		if len(e.Unavailable) > 0 {
			return true
		}
	}
	return false
}
