// Package modelfilter is the one model-filter grammar wick uses everywhere a
// model list is narrowed by a typed query: the wick provider's live model
// sets (wick.MatchFilter) and the omp/opencode "live from CLI" model lists.
// The FE mirrors it in @wick-fe/common-ui modelFilter.ts — change both.
//
// Grammar:
//   - terms are separated by whitespace and ALL must hold (AND);
//   - a term is a case-insensitive substring of the model's id/label;
//   - `a|b` inside one term means a OR b;
//   - a `!` or `-` prefix excludes: `!a|b` = neither a nor b.
//
// So `claude|gpt !mini` = (claude OR gpt) AND NOT mini. A blank query, a lone
// `!`/`-`, and empty alternatives (`a||b`, `a|`) are ignored.
package modelfilter

import "strings"

// Match reports whether hay (the model's id + label) satisfies query.
func Match(hay, query string) bool {
	hay = strings.ToLower(hay)
	for _, raw := range strings.Fields(strings.ToLower(query)) {
		exclude := strings.HasPrefix(raw, "-") || strings.HasPrefix(raw, "!")
		if exclude {
			raw = raw[1:]
		}
		alts := alternatives(raw)
		if len(alts) == 0 {
			continue
		}
		hit := false
		for _, a := range alts {
			if strings.Contains(hay, a) {
				hit = true
				break
			}
		}
		if exclude == hit {
			return false
		}
	}
	return true
}

// alternatives splits one term on `|`, dropping empty pieces.
func alternatives(term string) []string {
	var out []string
	for _, a := range strings.Split(term, "|") {
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}
