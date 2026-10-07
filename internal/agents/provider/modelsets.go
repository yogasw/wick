package provider

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
)

// modelsets.go is the generic "grouped picker" contract: a provider type can
// put any number of levels between the instance and the model it runs
// (wick: live set → vendor model; omp/opencode: provider → [account] →
// model). The handler and the CLI spawn path only talk to this registry, so
// a new provider implements ModelSets + RegisterModelSets and touches
// neither the handler nor the FE.
//
// A level is addressed by a PATH of segments. On the wire the path is one
// string, segments escaped and joined by '/'; a pin is "<path>@<model>".
// wick's historical pin "<entry>@<model>" is exactly a one-segment path, so
// it stays valid (entry ids carry no '/', '@' or '%').

// ModelChoice is one row of a picker level. Live marks a row with children:
// the picker drills into it by appending ID to the current path.
type ModelChoice struct {
	ID      string
	Label   string
	Default bool
	Desc    string
	Live    bool
	Caps    json.RawMessage
	// Unavailable greys the row out: the model is listed but the account
	// behind this level was refused it (model_not_found / no access).
	Unavailable bool
}

// SpawnPin is what a spawn runs for a decoded pin.
type SpawnPin struct {
	// Model is the concrete id handed to the CLI (--model).
	Model string
	// Account selects one account of the provider; "" = Auto (the CLI or
	// wick rotates).
	Account string
	// Provider is the first path segment for provider-grouped types.
	Provider string
}

// ModelSets exposes a provider type's grouped picker levels.
type ModelSets interface {
	// Sets is the level directly under the instance.
	Sets(ctx context.Context, ins Instance) ([]ModelChoice, error)
	// Expand lists the children of path; rows may themselves be Live.
	Expand(ctx context.Context, ins Instance, path []string) ([]ModelChoice, error)
	// Resolve maps a picked path + model to what the spawn runs. Must not
	// touch the network: it runs on the spawn path.
	Resolve(ins Instance, path []string, model string) (SpawnPin, error)
}

var (
	modelSetsMu       sync.RWMutex
	modelSetsRegistry = map[Type]ModelSets{}
)

// RegisterModelSets installs t's grouped picker. Last registration wins.
// nil removes the entry (tests).
func RegisterModelSets(t Type, s ModelSets) {
	modelSetsMu.Lock()
	defer modelSetsMu.Unlock()
	if s == nil {
		delete(modelSetsRegistry, t)
		return
	}
	modelSetsRegistry[t] = s
}

// ModelSetsFor returns t's grouped picker, ok=false when t has none (the
// flat model list applies).
func ModelSetsFor(t Type) (ModelSets, bool) {
	modelSetsMu.RLock()
	defer modelSetsMu.RUnlock()
	s, ok := modelSetsRegistry[t]
	return s, ok
}

// escapeSegment escapes one path segment: url path escaping plus '@', which
// PathEscape leaves alone but the pin grammar reserves.
func escapeSegment(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), "@", "%40")
}

// EncodePath joins segments into the wire form ("codex/akun%20A").
func EncodePath(path []string) string {
	out := make([]string, len(path))
	for i, s := range path {
		out[i] = escapeSegment(s)
	}
	return strings.Join(out, "/")
}

// DecodePath splits the wire form back into segments. Empty segments are
// dropped; a segment that fails to unescape is kept verbatim.
func DecodePath(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, seg := range strings.Split(s, "/") {
		if seg == "" {
			continue
		}
		if u, err := url.PathUnescape(seg); err == nil {
			seg = u
		}
		out = append(out, seg)
	}
	return out
}

// EncodePin is "<path>@<model>"; the model is verbatim (may hold '/' or '@').
func EncodePin(path []string, model string) string {
	return EncodePath(path) + "@" + model
}

// DecodePin splits a pin at its FIRST '@': the path is escaped so it never
// holds a raw '@', while a model id may. ok=false when there is no '@' (a
// flat model id, not a grouped pin).
func DecodePin(pin string) (path []string, model string, ok bool) {
	at := strings.IndexByte(pin, '@')
	if at < 0 {
		return nil, pin, false
	}
	return DecodePath(pin[:at]), pin[at+1:], true
}

// ResolvePin resolves a grouped pin through ins's registered ModelSets.
// ok=false when the pin is flat, the type has no ModelSets, or Resolve
// refused it — the caller then keeps its pre-registry behaviour.
func ResolvePin(ins *Instance, pin string) (SpawnPin, bool) {
	if ins == nil {
		return SpawnPin{}, false
	}
	path, model, grouped := DecodePin(pin)
	if !grouped {
		return SpawnPin{}, false
	}
	s, ok := ModelSetsFor(ins.Type)
	if !ok {
		return SpawnPin{}, false
	}
	p, err := s.Resolve(*ins, path, model)
	if err != nil || strings.TrimSpace(p.Model) == "" {
		return SpawnPin{}, false
	}
	return p, true
}

// FlattenModelSets walks t's picker levels down to the model rows, in
// picker order — what the composer picker would offer if every level were
// opened. A level that has an "Auto (rotation)" row is walked through that
// row only, so a provider with several accounts lists each model once
// (on Auto, where the instance-wide availability marks apply). For a
// settings page that shows the whole effective list (the provider page's
// live model list) through the very same ModelSets the picker drills.
func FlattenModelSets(ctx context.Context, s ModelSets, ins Instance) ([]ModelChoice, error) {
	rows, err := s.Sets(ctx, ins)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return flattenLevel(ctx, s, ins, nil, rows, 0)
}

func flattenLevel(ctx context.Context, s ModelSets, ins Instance, path []string, rows []ModelChoice, depth int) ([]ModelChoice, error) {
	if depth > 4 {
		return nil, nil
	}
	for _, r := range rows {
		if r.Live && r.ID == AutoAccount {
			rows = []ModelChoice{r}
			break
		}
	}
	var out []ModelChoice
	for _, r := range rows {
		if !r.Live {
			out = append(out, r)
			continue
		}
		next := append(append([]string{}, path...), r.ID)
		kids, err := s.Expand(ctx, ins, next)
		if err != nil {
			return out, err
		}
		sub, err := flattenLevel(ctx, s, ins, next, kids, depth+1)
		out = append(out, sub...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
