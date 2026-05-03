package connectors

import (
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/pkg/connector"
)

// userMasker binds an enc.Service + user UUID into the narrow
// connector.Masker interface so connectors can call c.Mask /
// c.MaskIgnoreCase without importing internal/enc or knowing the user
// UUID. Returns nil (treated as passthrough) when encryption is
// unavailable or globally disabled.
func userMasker(svc *enc.Service, userUUID string) connector.Masker {
	if svc == nil || svc.Disabled() {
		return nil
	}
	return &maskerAdapter{svc: svc, user: userUUID}
}

type maskerAdapter struct {
	svc  *enc.Service
	user string
}

func (m *maskerAdapter) Mask(data string, values []string, caseInsensitive bool) string {
	if caseInsensitive {
		return m.svc.MaskIgnoreCase(data, values, m.user)
	}
	return m.svc.Mask(data, values, m.user)
}

// unmaskMap returns a copy of m with every wick_enc_ token replaced by
// its plaintext. Empty input or service returns the input map verbatim.
// The whole map is rejected on the first decryption failure — partial
// substitutions would leave the connector running against a mix of
// plaintext and ciphertext credentials, which is the worst possible
// outcome.
func unmaskMap(svc *enc.Service, m map[string]string, userUUID string) (map[string]string, error) {
	if svc == nil || svc.Disabled() || len(m) == 0 {
		return m, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if !enc.IsToken(v) {
			out[k] = v
			continue
		}
		plain, err := svc.DecryptValue(v, userUUID)
		if err != nil {
			return nil, err
		}
		out[k] = plain
	}
	return out, nil
}

// collectSensitiveValues returns the plaintext values that should be
// masked in the connector's response — every config + input field
// whose `wick:"..."` tag carries `secret` or `encrypt`. Order is:
// configs first, then op-specific input. Empty values are skipped so
// Mask does not no-op-loop over them.
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
