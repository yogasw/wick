package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
)

// Creating an instance and immediately being told "connector not found" was
// the symptom; the cause is that manager visibility only ever asked two
// questions — admin-while-admin_see_all_connectors, or does a tag match —
// and a fresh row inherits the connector type's default tags, which are
// filter tags the creator usually does not carry. So the creator fell
// through both, on a row they own.
func TestOwnerCanAlwaysSeeTheirOwnInstance(t *testing.T) {
	svc := newConnectorsSvcForAPI(t, []connector.Module{historyModule("slack")})
	h := &Handler{connectors: svc}

	// A non-admin creator, so the admin bypass cannot mask the bug.
	creator := &entity.User{ID: "u-creator", Role: entity.RoleUser}
	row, err := svc.Create(t.Context(), "slack", "Mine", map[string]string{}, creator.ID)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("the detail handler no longer 404s for the creator", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/manager/api/connectors/slack/"+row.ID+"/history", nil)
		req.SetPathValue("key", "slack")
		req.SetPathValue("id", row.ID)
		req = req.WithContext(login.WithUser(req.Context(), creator, nil))
		rec := httptest.NewRecorder()
		h.apiConnectorHistory(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
}
