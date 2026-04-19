package fieldtype

import (
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// valueFor returns the value to pre-fill in the edit input. Secret
// values are never disclosed — the Secret widget uses its own empty
// default and does not call this.
func valueFor(v entity.Config) string {
	if v.IsSecret {
		return ""
	}
	return v.Value
}

func placeholderFor(v entity.Config) string {
	if v.IsSecret {
		return "Enter new value (current value is hidden)"
	}
	return ""
}

// dropdownOptions splits the pipe-separated Options column into a
// slice. Empty Options returns nil so the template renders an empty
// <select> without crashing.
func dropdownOptions(v entity.Config) []string {
	if v.Options == "" {
		return nil
	}
	return strings.Split(v.Options, "|")
}
