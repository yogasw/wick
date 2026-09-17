package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Creating a row used to seed its owner:<rowID> grant only for NON-admin
// creators, on the premise that "admins manage everything anyway". That
// premise holds only while admin_see_all_connectors is on, and it is a knob
// — one an operator can turn off today or a year from now, retroactively
// orphaning every row an admin ever made.
//
// It bites hardest on a custom connector, because that path also attaches
// custom:<key> with IsFilter=true. A filter tag flips the row from "untagged,
// so public" to "only tag-holders", and the one mechanism that would have
// handed the creator a tag was skipped precisely because they were an admin.
// Net effect: a connector visible in /admin/connectors and absent from
// wick_list, for the person who had just created it.
func TestCreateSeedsOwnerTagForAdminCreator(t *testing.T) {
	h, _, tagSvc, _ := newOwnerTagHandler(t)

	req := adminReq(t, http.MethodPost, "/manager/api/connectors/slack/rows", nil)
	req.SetPathValue("key", "slack")
	rec := httptest.NewRecorder()
	h.apiCreateConnectorRow(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ID == "" {
		t.Fatal("create returned no row id")
	}

	// adminReq authenticates as u-admin with the admin role — the exact
	// caller the old guard excluded.
	owns, err := tagSvc.UserOwnsConnector(t.Context(), "u-admin", out.ID)
	if err != nil {
		t.Fatalf("owner check: %v", err)
	}
	if !owns {
		t.Error("an admin who creates a row must carry its owner tag, " +
			"or the row is reachable only through admin_see_all_connectors")
	}

	// The grant is per-row and per-person: seeding it must not make the row
	// reachable by someone who simply exists.
	if owns, err := tagSvc.UserOwnsConnector(t.Context(), "u-stranger", out.ID); err != nil {
		t.Fatalf("stranger check: %v", err)
	} else if owns {
		t.Error("the owner tag leaked to a user who did not create the row")
	}
}
