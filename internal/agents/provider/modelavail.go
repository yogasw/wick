package provider

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/userconfig"
)

// modelavail.go remembers, per instance and account, which listed models the
// account was refused (model_not_found / "do not have access") and which one
// last actually ran. A CLI's model list says what EXISTS, not what this login
// may use (omp's `models --json` has no per-model access flag), so the only
// reliable signal is a real turn. The picker greys refused models out and
// never defaults to one; the default is the last model that worked.
//
// State is one small JSON file per instance under wick's own config dir —
// never inside the omp profile / opencode data dir. Refreshing the live model
// list resets it (the operator's "try again" button).

// modelStateEntry is one account's memory. Key "" is the Auto/only account.
type modelStateEntry struct {
	Unavailable map[string]string `json:"unavailable,omitempty"` // model → reason
	LastWorked  string            `json:"last_worked,omitempty"`
	WorkedAt    time.Time         `json:"worked_at,omitzero"`
}

type modelStateFile struct {
	Accounts map[string]*modelStateEntry `json:"accounts"`
}

var (
	modelStateMu    sync.Mutex
	modelStateCache = map[string]*modelStateFile{}
	// modelStateDir is swapped in tests.
	modelStateDir = func() (string, error) {
		root, err := userconfig.Dir(AppName())
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "providers", "model-state"), nil
	}
)

func modelStatePath(ins Instance) (string, error) {
	dir, err := modelStateDir()
	if err != nil {
		return "", err
	}
	name := strings.NewReplacer("/", "_", `\`, "_", "..", "_").Replace(ins.Name)
	return filepath.Join(dir, string(ins.Type)+"-"+name+".json"), nil
}

// loadModelState returns the cached state for ins (callers hold modelStateMu).
func loadModelState(ins Instance) (*modelStateFile, string) {
	path, err := modelStatePath(ins)
	if err != nil {
		return &modelStateFile{Accounts: map[string]*modelStateEntry{}}, ""
	}
	if st, ok := modelStateCache[path]; ok {
		return st, path
	}
	st := &modelStateFile{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Accounts == nil {
		st.Accounts = map[string]*modelStateEntry{}
	}
	modelStateCache[path] = st
	return st, path
}

func saveModelState(st *modelStateFile, path string) {
	if path == "" {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func modelStateEntryFor(st *modelStateFile, account string) *modelStateEntry {
	e := st.Accounts[account]
	if e == nil {
		e = &modelStateEntry{}
		st.Accounts[account] = e
	}
	return e
}

// AvailabilityAccount is the state key for one provider/account pair: the
// provider alone for Auto (or its only account), "<provider>#<account>" for a
// pinned account. Empty provider = the instance as a whole.
func AvailabilityAccount(prov, account string) string {
	if account == "" || account == AutoAccount {
		return prov
	}
	return prov + "#" + account
}

// AutoAccount is the path segment of the "Auto (rotation)" row.
const AutoAccount = "auto"

// MarkModelUnavailable records that account was refused model. A model that
// was the last one to work stops being the default.
func MarkModelUnavailable(ins Instance, account, model, reason string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	modelStateMu.Lock()
	defer modelStateMu.Unlock()
	st, path := loadModelState(ins)
	e := modelStateEntryFor(st, account)
	if e.Unavailable == nil {
		e.Unavailable = map[string]string{}
	}
	e.Unavailable[model] = strings.TrimSpace(reason)
	if e.LastWorked == model {
		e.LastWorked, e.WorkedAt = "", time.Time{}
	}
	saveModelState(st, path)
}

// MarkModelWorked records a turn that ran on model: it becomes the default
// and any earlier refusal is forgotten (plans change).
func MarkModelWorked(ins Instance, account, model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	modelStateMu.Lock()
	defer modelStateMu.Unlock()
	st, path := loadModelState(ins)
	e := modelStateEntryFor(st, account)
	_, wasRefused := e.Unavailable[model]
	if e.LastWorked == model && !wasRefused {
		return // hot path: nothing changed, skip the write
	}
	delete(e.Unavailable, model)
	e.LastWorked, e.WorkedAt = model, time.Now()
	saveModelState(st, path)
}

// ModelUnavailable reports whether account was refused model, with the reason.
func ModelUnavailable(ins Instance, account, model string) (string, bool) {
	modelStateMu.Lock()
	defer modelStateMu.Unlock()
	st, _ := loadModelState(ins)
	if e := st.Accounts[account]; e != nil {
		r, ok := e.Unavailable[model]
		return r, ok
	}
	return "", false
}

// LastWorkedModel is the model account last ran successfully, "" if none.
func LastWorkedModel(ins Instance, account string) string {
	modelStateMu.Lock()
	defer modelStateMu.Unlock()
	st, _ := loadModelState(ins)
	if e := st.Accounts[account]; e != nil {
		return e.LastWorked
	}
	return ""
}

// ResetModelAvailability forgets every refusal for ins (live models refresh).
// The last-worked default survives: it was proven, not guessed.
func ResetModelAvailability(ins Instance) {
	modelStateMu.Lock()
	defer modelStateMu.Unlock()
	st, path := loadModelState(ins)
	changed := false
	for _, e := range st.Accounts {
		if len(e.Unavailable) > 0 {
			e.Unavailable, changed = nil, true
		}
	}
	if changed {
		saveModelState(st, path)
	}
}

// ApplyAvailability greys out refused models and picks the default: the
// last model that worked for account, else the first row not refused.
// Rows that are Live (sub-levels) are left alone. Order is preserved.
func ApplyAvailability(ins Instance, account string, rows []ModelChoice) []ModelChoice {
	if len(rows) == 0 {
		return rows
	}
	modelStateMu.Lock()
	st, _ := loadModelState(ins)
	var unavailable map[string]string
	last := ""
	if e := st.Accounts[account]; e != nil {
		// Copied under the lock: Mark* mutate this map in place.
		unavailable, last = maps.Clone(e.Unavailable), e.LastWorked
	}
	modelStateMu.Unlock()

	out := make([]ModelChoice, len(rows))
	copy(out, rows)
	lastIdx, firstOK, pinIdx := -1, -1, -1
	for i := range out {
		if out[i].Live {
			continue
		}
		if reason, bad := unavailable[out[i].ID]; bad {
			out[i].Unavailable = true
			if out[i].Desc == "" {
				out[i].Desc = "Not available for this account"
				if reason != "" {
					out[i].Desc += " — " + reason
				}
			}
		}
		if out[i].Unavailable {
			out[i].Default = false
			continue
		}
		if out[i].ID == last && last != "" {
			lastIdx = i
		}
		if firstOK < 0 {
			firstOK = i
		}
		if out[i].Default && pinIdx < 0 {
			pinIdx = i
		}
	}
	pick := lastIdx
	if pick < 0 {
		pick = pinIdx
	}
	if pick < 0 {
		pick = firstOK
	}
	for i := range out {
		if !out[i].Live {
			out[i].Default = i == pick
		}
	}
	return out
}

// IsModelAccessError reports whether a turn error means "this account may
// not use this model" rather than a transient failure.
func IsModelAccessError(msg string) bool {
	low := strings.ToLower(msg)
	for _, s := range []string{
		"model_not_found",
		"do not have access",
		"does not have access",
		"not have access to the model",
		"is not supported when using codex with a chatgpt account",
		// opencode (Zen) upstream: "Upstream request failed: Model access is disabled"
		"model access is disabled",
	} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// ModelUnavailableMessage is the user-facing text for a refused model, with
// the account plan when the CLI reported one.
func ModelUnavailableMessage(model, plan string) string {
	short := model
	if i := strings.LastIndexByte(short, '/'); i >= 0 {
		short = short[i+1:]
	}
	if plan = strings.TrimSpace(plan); plan != "" {
		return fmt.Sprintf("%s tidak tersedia untuk akun %s ini — pilih model lain.", short, plan)
	}
	return fmt.Sprintf("%s tidak tersedia untuk akun ini — pilih model lain.", short)
}

// SetModelStateDirForTest points the availability store at dir and empties
// its cache; the returned func restores both. Tests only (other packages
// cannot reach the unexported seam).
func SetModelStateDirForTest(dir string) (restore func()) {
	modelStateMu.Lock()
	prev := modelStateDir
	modelStateDir = func() (string, error) { return dir, nil }
	modelStateCache = map[string]*modelStateFile{}
	modelStateMu.Unlock()
	return func() {
		modelStateMu.Lock()
		modelStateDir = prev
		modelStateCache = map[string]*modelStateFile{}
		modelStateMu.Unlock()
	}
}
