package connectors

import (
	"sync"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/pkg/connector"
)

// newMaskerAdapter builds the per-call Masker the framework hands to
// connector.Ctx. It both implements c.Mask / c.MaskIgnoreCase AND
// records every plaintext that flows through it, so the post-Execute
// auto-mask phase can replay those plaintexts against the marshaled
// response in one pass — covering values the LLM passed in as
// wick_enc_ tokens (decrypted into plaintext for ExecuteFunc) and
// dynamic values the connector explicitly tagged via c.Mask.
//
// Returns nil when encryption is unavailable or globally disabled —
// connector.Ctx treats a nil Masker as passthrough, and Execute skips
// the mask phase under the same condition.
func newMaskerAdapter(svc *enc.Service, userUUID string) *maskerAdapter {
	if svc == nil || svc.Disabled() {
		return nil
	}
	return &maskerAdapter{svc: svc, user: userUUID}
}

type maskerAdapter struct {
	svc  *enc.Service
	user string

	mu   sync.Mutex
	seen []string
}

func (m *maskerAdapter) Mask(data string, values []string, caseInsensitive bool) string {
	if m == nil {
		return data
	}
	m.add(values)
	if caseInsensitive {
		return m.svc.MaskIgnoreCase(data, values, m.user)
	}
	return m.svc.Mask(data, values, m.user)
}

// add records plaintexts for the post-Execute mask sweep. Empty values
// are dropped so Mask does not no-op-loop over them.
func (m *maskerAdapter) add(values []string) {
	if m == nil || len(values) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range values {
		if v != "" {
			m.seen = append(m.seen, v)
		}
	}
}

// snapshot returns a deduped copy of every plaintext recorded so far.
// Called once after ExecuteFunc returns; the order of `seen` is
// preserved for first occurrence so identical plaintexts collapse to
// one Mask invocation.
func (m *maskerAdapter) snapshot() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.seen))
	dedup := make(map[string]struct{}, len(m.seen))
	for _, v := range m.seen {
		if _, ok := dedup[v]; ok {
			continue
		}
		dedup[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// unmaskMap returns a copy of m with every wick_enc_ token replaced by
// its plaintext, plus the list of plaintexts produced by that
// substitution. Empty input or service returns the input map verbatim
// and a nil decrypted list. The whole map is rejected on the first
// decryption failure — partial substitutions would leave the connector
// running against a mix of plaintext and ciphertext credentials, which
// is the worst possible outcome.
//
// The decrypted list is what lets the auto-mask phase round-trip a
// wick_enc_ token regardless of whether the receiving field carries
// the `secret` tag: any plaintext that came in via a token must leave
// in token form too, otherwise the LLM contract that "wick_enc_ is
// opaque" silently breaks the moment a connector echoes such a value.
func unmaskMap(svc *enc.Service, m map[string]string, userUUID string) (map[string]string, []string, error) {
	if svc == nil || svc.Disabled() || len(m) == 0 {
		return m, nil, nil
	}
	out := make(map[string]string, len(m))
	var decrypted []string
	for k, v := range m {
		if !enc.IsToken(v) {
			out[k] = v
			continue
		}
		plain, err := svc.DecryptValue(v, userUUID)
		if err != nil {
			return nil, nil, err
		}
		out[k] = plain
		if plain != "" {
			decrypted = append(decrypted, plain)
		}
	}
	return out, decrypted, nil
}

// collectSensitiveValues returns the plaintext values that should be
// masked in the connector's response — every config + input field
// whose `wick:"..."` tag carries `secret`. Order is: configs first,
// then op-specific input. Empty values are skipped so Mask does not
// no-op-loop over them.
func collectSensitiveValues(mod connector.Module, op *connector.Operation, configs, input map[string]string) []string {
	var out []string
	for _, c := range mod.Configs {
		if !c.IsSecret {
			continue
		}
		if v := configs[c.Key]; v != "" {
			out = append(out, v)
		}
	}
	for _, c := range op.Input {
		if !c.IsSecret {
			continue
		}
		if v := input[c.Key]; v != "" {
			out = append(out, v)
		}
	}
	return out
}
