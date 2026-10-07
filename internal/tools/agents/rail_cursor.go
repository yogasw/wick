package agents

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/tools/agents/view"
)

// railKey is where a chat sits in the sidebar's order — exactly the key
// orderSidebarIDs sorts by: running first, then newest first, then id.
//
// The untracked rail pages by this key, not by a row count. A count is only
// right while the client holds exactly the rows the server would put above
// it, and a chat that leaves the untracked set some other way (attached by
// somebody else, made a ticket off-board, deleted) stays drawn on a page
// already loaded — every such ghost moved the next offset one row too far
// and skipped a real chat for the rest of the visit. A key says "after THIS
// row", which no row the client holds or lost can shift.
type railKey struct {
	running bool
	at      int64
	id      string
}

func sidebarKey(id string, s session.Session, l view.SessionLifecycleVM) railKey {
	return railKey{
		running: view.IsRunningStatus(view.SidebarRowStatus(s, l)),
		at:      view.SidebarLastActiveMs(s, l),
		id:      id,
	}
}

// before reports whether k sorts ahead of o in the sidebar's order — the
// same comparison orderSidebarIDs makes, so "after the cursor" and "later in
// the list" can never disagree.
func (k railKey) before(o railKey) bool {
	if k.running != o.running {
		return k.running
	}
	if k.at != o.at {
		return k.at > o.at
	}
	return k.id < o.id
}

// encode is the opaque cursor the client sends back as ?untracked_after=.
// It is the server's to read: the client never builds or inspects one.
func (k railKey) encode() string {
	r := "0"
	if k.running {
		r = "1"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(r + "|" + strconv.FormatInt(k.at, 10) + "|" + k.id))
}

var errBadRailCursor = errors.New("invalid untracked_after cursor")

// decodeRailCursor reads a cursor made by encode. Anything else is refused
// rather than guessed at: a cursor read wrong would page from the wrong
// place without anyone noticing.
func decodeRailCursor(s string) (railKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return railKey{}, errBadRailCursor
	}
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) != 3 || (parts[0] != "0" && parts[0] != "1") || parts[2] == "" {
		return railKey{}, errBadRailCursor
	}
	at, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || at < 0 {
		return railKey{}, errBadRailCursor
	}
	return railKey{running: parts[0] == "1", at: at, id: parts[2]}, nil
}
