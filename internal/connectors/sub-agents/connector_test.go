package subagents

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
)

// Fixed=true is what makes the connector non-duplicable: wick seeds
// exactly one row and the manager UI hides "+ New row" / Duplicate /
// Delete. Two instances of a delegation surface would be meaningless —
// there is one delegation service behind it.
func TestConnectorIsSingleInstance(t *testing.T) {
	if !Meta().Fixed {
		t.Fatal("sub-agents must be Fixed so only one instance can exist")
	}
}

// Deliberately NOT tags.System. System is IsFilter+IsSystem, which no
// user can carry, so it hides the row from every non-admin — and
// delegation that only admins can see is delegation that does not exist
// for the people who need it.
func TestConnectorIsVisibleToEveryone(t *testing.T) {
	m := Module(Deps{})
	var names []string
	for _, tg := range m.Meta.DefaultTags {
		names = append(names, tg.Name)
		if tg.Name == tags.System.Name {
			t.Fatal("sub-agents must not carry the System tag; it would hide the feature from every non-admin")
		}
	}
	found := false
	for _, n := range names {
		if n == tags.Connector.Name {
			found = true
		}
	}
	if !found {
		t.Fatalf("tags = %v, want the Connector group tag", names)
	}
}

// The agent picks an op by reading its description, so every op needs a
// key and a description that says when to use it. A missing description
// makes the op unusable in practice even though it is callable.
func TestEveryOperationIsDescribed(t *testing.T) {
	m := Module(Deps{})
	seen := map[string]bool{}
	count := 0
	for _, cat := range m.Operations {
		for _, op := range cat.Ops {
			count++
			if op.Key == "" {
				t.Fatal("operation with an empty key")
			}
			if seen[op.Key] {
				t.Fatalf("duplicate op key %q", op.Key)
			}
			seen[op.Key] = true
			if len(strings.TrimSpace(op.Description)) < 40 {
				t.Fatalf("op %q needs a description that says when to use it", op.Key)
			}
		}
	}
	for _, want := range []string{"list_agents", "delegate", "collect", "create_agent", "tasks"} {
		if !seen[want] {
			t.Fatalf("missing op %q", want)
		}
	}
	if count != 5 {
		t.Fatalf("got %d ops, want 5", count)
	}
}

// A deployment without delegation wired must fail every op with a clear
// message rather than panicking on a nil service.
func TestOpsFailClosedWithoutService(t *testing.T) {
	h := newHandlers(Deps{})
	c := &connector.Ctx{}
	for name, fn := range map[string]func(*connector.Ctx) (any, error){
		"list_agents":  h.listAgents,
		"delegate":     h.delegate,
		"collect":      h.collect,
		"create_agent": h.createAgent,
		"tasks":        h.tasks,
	} {
		if _, err := fn(c); err == nil {
			t.Fatalf("%s returned no error with delegation unconfigured", name)
		}
	}
}

// The resolver is what bridges boot ordering — the connector registers
// before the delegation service exists. A nil-returning resolver must be
// as safe as no resolver at all.
func TestNilResolverIsTreatedAsUnavailable(t *testing.T) {
	d := Deps{Service: func() *delegation.Service { return nil }}
	if err := d.ready(); err == nil {
		t.Fatal("a resolver that yields nil must report unavailable")
	}
}
