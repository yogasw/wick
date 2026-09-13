package view

import (
	"encoding/json"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func tagsToJSON(tags []*entity.Tag) string {
	out := make([]map[string]any, len(tags))
	for i, t := range tags {
		row := map[string]any{
			"id":        t.ID,
			"name":      t.Name,
			"is_group":  t.IsGroup,
			"is_filter": t.IsFilter,
		}
		if t.DisplayName != "" {
			row["display_name"] = t.DisplayName
		}
		out[i] = row
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// searchHaystack builds the lowercased blob the per-page search box matches
// against (see AccessSearch and access.js). Keeping it server-side means the
// tag NAMES a row carries are searchable even though the row only renders tag
// ids in its picker.
func searchHaystack(parts ...string) string {
	return strings.ToLower(strings.Join(parts, " "))
}

// TagNames resolves tag ids to names for a row's search blob. Unknown ids are
// skipped — a tag deleted under a row should not make it unsearchable.
func TagNames(all []*entity.Tag, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]string, len(all))
	for _, t := range all {
		byID[t.ID] = t.Name
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
		}
	}
	return out
}
