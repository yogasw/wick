package plugin

import (
	"testing"
	"time"
)

func TestManagerEvictsIdle(t *testing.T) {
	killed := map[string]bool{}
	m := &Manager{
		idleTimeout: 10 * time.Millisecond,
		entries:     map[string]*entry{},
		killFn:      func(key string) { killed[key] = true },
		now:         func() time.Time { return time.Unix(100, 0) },
	}
	m.entries["github"] = &entry{lastUsed: time.Unix(0, 0)}
	m.entries["slack"] = &entry{lastUsed: time.Unix(100, 0)}

	m.sweep()

	if !killed["github"] {
		t.Fatal("expected idle plugin github to be killed")
	}
	if killed["slack"] {
		t.Fatal("fresh plugin slack must not be killed")
	}
	if _, ok := m.entries["github"]; ok {
		t.Fatal("killed entry must be removed from the map")
	}
}
