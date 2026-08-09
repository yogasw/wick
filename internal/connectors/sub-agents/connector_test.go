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
	for _, want := range []string{"list_agents", "delegate", "continue", "collect", "progress", "report_result", "incident", "create_agent", "tasks", "message", "reply", "stop", "list_access"} {
		if !seen[want] {
			t.Fatalf("missing op %q", want)
		}
	}
	if count != 13 {
		t.Fatalf("got %d ops, want 13", count)
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
		"message":      h.message,
		"reply":        h.reply,
		"stop":         h.stop,
		"list_access":   h.listAccess,
		"report_result": h.reportResult,
		"incident":      h.incident,
		"continue":      h.continueDelegation,
		"progress":      h.progress,
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

// A mode the model cannot discover is a mode it will never use. Both
// surfaces that accept one must name every value, since there is nowhere
// else for a caller to learn them.
func TestMemoryModeIsAdvertisedWithEveryValue(t *testing.T) {
	for _, opKey := range []string{"delegate", "create_agent"} {
		op := findOp(t, opKey)
		field, ok := fieldByName(op, "memory_mode")
		if !ok {
			t.Fatalf("op %q has no memory_mode input", opKey)
		}
		for _, want := range delegation.MemoryModes() {
			if !strings.Contains(field, want) {
				t.Fatalf("op %q memory_mode description omits %q: %s", opKey, want, field)
			}
		}
	}
}

// fieldByName returns an op input's description by field key.
func fieldByName(op *connector.Operation, key string) (string, bool) {
	for _, f := range op.Input {
		if f.Key == key {
			return f.Description, true
		}
	}
	return "", false
}

// findOp returns the named operation from the module, or fails the test.
// Two tests below need it and a second copy would be a second chance to
// look up the wrong op.
func findOp(t *testing.T, key string) *connector.Operation {
	t.Helper()
	for _, cat := range Module(Deps{}).Operations {
		for i := range cat.Ops {
			if cat.Ops[i].Key == key {
				return &cat.Ops[i]
			}
		}
	}
	t.Fatalf("op %q is missing", key)
	return nil
}

// create_agent is the only way an agent can define a role, so a field it
// cannot reach is a role it cannot get right. This pins the full set
// against the entity — a column added later without an input here shows
// up as a failure rather than as a silently unreachable setting.
func TestCreateAgentCoversEveryProfileField(t *testing.T) {
	op := findOp(t, "create_agent")
	got := map[string]bool{}
	for _, f := range op.Input {
		got[f.Key] = true
	}
	want := []string{
		"key", "name", "description", "system_prompt", "provider", "model",
		"max_turns", "max_tokens", "allowed_tags", "allowed_native_tools",
		"strict_mcp", "can_delegate", "allow_take_over", "mode", "workspace",
		"icon", "disabled", "locked",
	}
	for _, k := range want {
		if !got[k] {
			t.Fatalf("create_agent cannot set %q", k)
		}
	}
}

// Two of those fields are stored and read by nobody. Saying so in the desc
// is the difference between an inert setting and a false promise the LLM
// will act on.
func TestUnwiredFieldsSaySo(t *testing.T) {
	op := findOp(t, "create_agent")
	for _, f := range op.Input {
		if f.Key != "allowed_native_tools" && f.Key != "strict_mcp" {
			continue
		}
		if !strings.Contains(strings.ToUpper(f.Description), "NOT ENFORCED") {
			t.Fatalf("%q must say it is not enforced yet, got %q", f.Key, f.Description)
		}
	}
}

// The whole point of continue is that the model reaches for it INSTEAD of
// spawning a stranger. Both halves have to be said: what it does, and
// that the task field is the next step rather than the brief again — a
// restated brief makes a resumed agent start over, which looks identical
// to the bug this op exists to fix.
func TestContinueOpTellsTheModelHowItDiffersFromDelegate(t *testing.T) {
	op := findOp(t, "continue")
	desc := strings.ToLower(op.Description)
	for _, want := range []string{"same session", "next instruction", "needs_followup", "message"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("continue description never mentions %q — the model cannot tell when to pick it:\n%s", want, op.Description)
		}
	}
	// Destructive would default the toggle OFF on every row, leaving a
	// leader able to start work it cannot carry forward.
	if op.Destructive {
		t.Fatal("continue must not be destructive: it adds work, it never discards any")
	}
}

// Continuing must not offer to change what the agent IS. Those were
// settled at spawn, and altering one mid-life continues a different agent
// than the transcript being resumed belongs to.
func TestContinueOpCannotRedefineTheAgent(t *testing.T) {
	op := findOp(t, "continue")
	for _, forbidden := range []string{"profile", "workspace", "memory_mode"} {
		if _, ok := fieldByName(op, forbidden); ok {
			t.Fatalf("continue accepts %q — that continues a DIFFERENT agent than the one being resumed", forbidden)
		}
	}
	for _, required := range []string{"delegation_id", "task"} {
		if _, ok := fieldByName(op, required); !ok {
			t.Fatalf("continue has no %q input", required)
		}
	}
}

// delegate's continue_id is a shortcut onto the same handler. It has to
// stay ONE sentence: that description is what steers the model between
// spawning and following up, and a second mode explained at length there
// is what makes it choose wrong.
func TestDelegateContinueIDPointsAtTheContinueOp(t *testing.T) {
	op := findOp(t, "delegate")
	desc, ok := fieldByName(op, "continue_id")
	if !ok {
		t.Fatal("delegate has no continue_id input")
	}
	if !strings.Contains(strings.ToLower(desc), "continue") {
		t.Fatalf("continue_id never names the continue op: %q", desc)
	}
}
