package tool

import (
	"fmt"
	"strings"
)

// ValidateModules is called by wick at boot. It checks that every
// module's Meta has the minimum required fields (Key, Name, Icon) and
// that no two modules share the same Key. A non-nil error means the
// process should refuse to start.
//
// Path is derived by wick from Key ("/tools/{Key}") after validation;
// registrations must leave Tool.Path zero.
func ValidateModules(modules []Module) error {
	seen := make(map[string]string) // key -> owning tool name
	for _, m := range modules {
		t := m.Meta
		if err := validateTool(t); err != nil {
			return err
		}
		if prev, dup := seen[t.Key]; dup {
			return fmt.Errorf("tool: duplicate Key %q used by %q and %q", t.Key, prev, t.Name)
		}
		seen[t.Key] = t.Name
	}
	return nil
}

func validateTool(t Tool) error {
	var missing []string
	if strings.TrimSpace(t.Key) == "" {
		missing = append(missing, "Key")
	}
	if strings.TrimSpace(t.Name) == "" {
		missing = append(missing, "Name")
	}
	if strings.TrimSpace(t.Icon) == "" {
		missing = append(missing, "Icon")
	}
	if strings.TrimSpace(t.Path) != "" {
		return fmt.Errorf("tool %q: Path must be empty — wick derives it from Key", t.Name)
	}
	if strings.ContainsAny(t.Key, "/ ") {
		return fmt.Errorf("tool %q: Key %q must not contain '/' or spaces", t.Name, t.Key)
	}
	if len(missing) == 0 {
		return nil
	}
	label := t.Name
	if label == "" {
		label = "tool"
	}
	return fmt.Errorf("tool %q: missing required field(s): %s", label, strings.Join(missing, ", "))
}
