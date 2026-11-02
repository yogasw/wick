package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/pkg/connector"
)

func getDetailOAuth(t *testing.T, h *Handler, key, id string) *connectorOAuthJSON {
	t.Helper()
	rec := httptest.NewRecorder()
	h.apiConnectorDetail(rec, pathReq(t, http.MethodGet, "/x", key, id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var got connectorDetailJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OAuth == nil {
		t.Fatalf("expected oauth meta")
	}
	return got.OAuth
}

// An SSO row starts with the shared credential on (no notice); turned off,
// a viewer with no account of their own is told to connect, and turning it
// back on drops the notice.
func TestAPISharedCredentialToggleAndConnectNotice(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{"client_id": "cid"}, "u-admin")
	if err := svc.SetAccessPolicy(t.Context(), row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	seedAccount(t, svc, row.ID, "u-someone-else")

	got := getDetailOAuth(t, h, "slack", row.ID)
	if !got.SharedCredential || got.NeedsConnect {
		t.Errorf("rows default to shared credential on with no notice, got %+v", got)
	}

	off := httptest.NewRecorder()
	h.apiSetConnectorSharedCredential(off, pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]bool{"allow_shared_credential": false}))
	if off.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", off.Code, off.Body.String())
	}
	got = getDetailOAuth(t, h, "slack", row.ID)
	if got.SharedCredential {
		t.Errorf("shared credential should be off")
	}
	if !got.NeedsConnect {
		t.Errorf("viewer without an account must see the connect notice")
	}

	rec := httptest.NewRecorder()
	h.apiSetConnectorSharedCredential(rec, pathReq(t, http.MethodPost, "/x", "slack", row.ID, map[string]bool{"allow_shared_credential": true}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	got = getDetailOAuth(t, h, "slack", row.ID)
	if !got.SharedCredential || got.NeedsConnect {
		t.Errorf("shared credential on: no connect notice, got %+v", got)
	}
}

func TestAPISharedCredentialNoticeHiddenWithOwnAccount(t *testing.T) {
	h, svc := newAdminHandler(t)
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{"client_id": "cid"}, "u-admin")
	if err := svc.SetAccessPolicy(t.Context(), row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	seedAccount(t, svc, row.ID, "u-admin")
	if got := getDetailOAuth(t, h, "slack", row.ID); got.NeedsConnect {
		t.Errorf("a viewer with their own account needs no connect")
	}
}

func TestAPISharedCredentialRejectsNonOAuth(t *testing.T) {
	svc := newConnectorsSvcForAPI(t, []connector.Module{apiDetailModule("plain")})
	h := &Handler{connectors: svc}
	row, _ := svc.Create(t.Context(), "plain", "Prod", map[string]string{}, "u-admin")
	rec := httptest.NewRecorder()
	h.apiSetConnectorSharedCredential(rec, pathReq(t, http.MethodPost, "/x", "plain", row.ID, map[string]bool{"allow_shared_credential": true}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
