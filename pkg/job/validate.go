package job

import (
	"fmt"
	"strings"
)

// ValidateJobs is called by wick at boot. It checks that each Module's
// Meta has the minimum required fields (Key, Name, Icon), that Run is
// non-nil, and that no two jobs share the same Key. A non-nil error
// means the process should refuse to start.
func ValidateJobs(mods []Module) error {
	seen := make(map[string]string) // key -> owning job name
	for _, mod := range mods {
		if err := validateMeta(mod.Meta); err != nil {
			return err
		}
		if mod.Run == nil {
			return fmt.Errorf("job %q: Run is nil", mod.Meta.Key)
		}
		if prev, dup := seen[mod.Meta.Key]; dup {
			return fmt.Errorf("job: duplicate Key %q used by %q and %q", mod.Meta.Key, prev, mod.Meta.Name)
		}
		seen[mod.Meta.Key] = mod.Meta.Name
	}
	return nil
}

func validateMeta(m Meta) error {
	var missing []string
	if strings.TrimSpace(m.Key) == "" {
		missing = append(missing, "Key")
	}
	if strings.TrimSpace(m.Name) == "" {
		missing = append(missing, "Name")
	}
	if strings.TrimSpace(m.Icon) == "" {
		missing = append(missing, "Icon")
	}
	if len(missing) == 0 {
		return nil
	}
	label := m.Name
	if label == "" {
		label = "job"
	}
	return fmt.Errorf("job %q: missing required field(s): %s", label, strings.Join(missing, ", "))
}
