package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/accessreq"
)

func TestAccessSummaryHidesApproverNames(t *testing.T) {
	v := accessreq.View{ID: "req-1", Status: "pending", Pending: 1, Items: []accessreq.ItemView{{
		ConnectorKey: "acme", OpKey: "send", Status: accessreq.ItemPending, DecidedBy: "Person B",
		ApproversText: "members of @ops",
		Approvers:     []accessreq.Person{{ID: "u1", Name: "Person A"}, {ID: "u2", Name: "Person B"}},
	}}}
	b, _ := json.Marshal(accessSummary(v))
	s := string(b)
	for _, name := range []string{"Person A", "Person B", `"approvers"`, `"decided_by"`} {
		if strings.Contains(s, name) {
			t.Fatalf("summary leaks %s: %s", name, s)
		}
	}
	if !strings.Contains(s, `"approver_group":"members of @ops"`) || !strings.Contains(s, `"reply_note"`) {
		t.Fatalf("summary misses group or reply note: %s", s)
	}
}
