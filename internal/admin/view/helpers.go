package view

import (
	"encoding/json"
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
		out[i] = map[string]any{
			"id":        t.ID,
			"name":      t.Name,
			"is_group":  t.IsGroup,
			"is_filter": t.IsFilter,
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}
