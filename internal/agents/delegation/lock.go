package delegation

import (
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/entity"
)

// ErrProfileLocked reports a mutation aimed at a role whose Locked flag
// is set.
var ErrProfileLocked = errors.New("role is locked")

// CheckMutable reports whether a role may be changed at all.
//
// Pure on purpose: the web save path, the web delete path and the MCP
// create_agent op all have to apply exactly the same rule, and a rule
// written three times becomes three rules. A nil existing row is a
// create, which is always allowed.
//
// The message names the way out rather than only refusing — an LLM told
// nothing but "no" retries the identical payload.
func CheckMutable(existing *entity.AgentProfile) error {
	if existing == nil || !existing.Locked {
		return nil
	}
	return fmt.Errorf("%w: %q is locked; unlock it in the web UI (Sub-agents → %s) by unticking Locked and saving, then retry",
		ErrProfileLocked, existing.Key, existing.Key)
}
