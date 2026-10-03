package teamagents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
)

func TestModuleShape(t *testing.T) {
	m := Module(Deps{})
	if m.Meta.Key != team.ManageAgentsKey {
		t.Fatalf("key = %q", m.Meta.Key)
	}
	if team.TierOf(m.Meta.DefaultTags) != team.TierPlatform {
		t.Error("the connector must be Platform-tagged so the owner's catalog carries it")
	}
	want := map[string]bool{"list": false, "create": true, "update_persona": true, "set_access": true, "schedule": true}
	n := 0
	for _, cat := range m.Operations {
		for _, op := range cat.Ops {
			d, ok := want[op.Key]
			if !ok {
				t.Errorf("unexpected op %q", op.Key)
			}
			if op.Destructive != d {
				t.Errorf("%s destructive = %v", op.Key, op.Destructive)
			}
			n++
		}
	}
	if n != len(want) {
		t.Errorf("%d ops, want %d", n, len(want))
	}
}

func TestParseGrants(t *testing.T) {
	if gs, err := ParseGrants(""); err != nil || gs != nil {
		t.Errorf("empty = %v %v", gs, err)
	}
	gs, err := ParseGrants(`[{"connector_id":"n1","level":"read"}]`)
	if err != nil || len(gs) != 1 || gs[0].Level != team.LevelRead {
		t.Errorf("parse = %v %v", gs, err)
	}
	if _, err := ParseGrants(`{"oops":1}`); err == nil {
		t.Error("non-array must fail")
	}
}
