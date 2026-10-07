package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// The per-account override has to survive the real dispatch path, not just
// the resolver. The gate that rejects a disabled operation used to run
// BEFORE any account was loaded and return there, so a forced-ON override
// was unreachable by construction — the resolver would have said yes and
// nobody would ever have asked it.
func TestExecuteHonoursAccountOpOverrides(t *testing.T) {
	setup := func(t *testing.T) (*connectors.Service, entity.Connector, string) {
		t.Helper()
		svc := newConnectorsSvcForAPI(t, []connector.Module{historyModule("slack")})
		row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := svc.SaveAccount(t.Context(), row.ID, "u-admin", "U1", "yoga.setiawan", "xoxp-test"); err != nil {
			t.Fatalf("save account: %v", err)
		}
		accs, err := svc.ListAccounts(t.Context(), row.ID)
		if err != nil || len(accs) != 1 {
			t.Fatalf("list accounts: %v (%d)", err, len(accs))
		}
		return svc, *row, accs[0].ID
	}

	run := func(t *testing.T, svc *connectors.Service, rowID, accountID string) error {
		t.Helper()
		_, err := svc.Execute(t.Context(), connectors.ExecuteParams{
			ConnectorID:  rowID,
			OperationKey: "send",
			Input:        map[string]string{"text": "hi"},
			Source:       entity.ConnectorRunSourceTest,
			UserID:       "u-admin",
			IsAdmin:      true,
			AccountID:    accountID,
		})
		return err
	}

	t.Run("instance off, account inherits: refused", func(t *testing.T) {
		svc, row, accID := setup(t)
		if err := svc.SetOperationEnabled(t.Context(), row.ID, "send", false); err != nil {
			t.Fatalf("disable op: %v", err)
		}
		if err := run(t, svc, row.ID, accID); err == nil {
			t.Fatal("an account that inherits an instance-off operation must be refused")
		}
	})

	t.Run("instance off, account forces on: allowed", func(t *testing.T) {
		svc, row, accID := setup(t)
		if err := svc.SetOperationEnabled(t.Context(), row.ID, "send", false); err != nil {
			t.Fatalf("disable op: %v", err)
		}
		if err := svc.SetAccountOpOverride(t.Context(), accID, "send", connectors.AccountOpOn); err != nil {
			t.Fatalf("override on: %v", err)
		}
		if err := run(t, svc, row.ID, accID); err != nil {
			t.Fatalf("a forced-on override must reach dispatch: %v", err)
		}
	})

	t.Run("instance on, account forces off: refused", func(t *testing.T) {
		svc, row, accID := setup(t)
		if err := svc.SetAccountOpOverride(t.Context(), accID, "send", connectors.AccountOpOff); err != nil {
			t.Fatalf("override off: %v", err)
		}
		err := run(t, svc, row.ID, accID)
		if err == nil {
			t.Fatal("a forced-off override must block the account")
		}
		// The message has to name the account, or the operator reads it as
		// the instance being off and goes looking in the wrong place.
		if got := err.Error(); !strings.Contains(got, "yoga.setiawan") {
			t.Errorf("error should name the account, got %q", got)
		}
	})

	t.Run("clearing the override returns the account to the instance", func(t *testing.T) {
		svc, row, accID := setup(t)
		if err := svc.SetAccountOpOverride(t.Context(), accID, "send", connectors.AccountOpOff); err != nil {
			t.Fatalf("override off: %v", err)
		}
		if err := svc.SetAccountOpOverride(t.Context(), accID, "send", connectors.AccountOpInherit); err != nil {
			t.Fatalf("clear override: %v", err)
		}
		if err := run(t, svc, row.ID, accID); err != nil {
			t.Fatalf("back to inheriting an instance-on operation: %v", err)
		}
		// Inherit must DELETE the key, not store the instance's current
		// answer — otherwise the account stops following later changes.
		acc, err := svc.GetAccount(t.Context(), accID)
		if err != nil {
			t.Fatalf("get account: %v", err)
		}
		if len(connectors.AccountOpOverrides(acc)) != 0 {
			t.Errorf("inherit should leave no override behind, got %+v", connectors.AccountOpOverrides(acc))
		}
	})

	t.Run("an unknown state is refused rather than guessed", func(t *testing.T) {
		_, _, accID := setup(t)
		svc := newConnectorsSvcForAPI(t, []connector.Module{historyModule("slack")})
		if err := svc.SetAccountOpOverride(t.Context(), accID, "send", "maybe"); err == nil {
			t.Fatal("an unknown state must error")
		}
	})
}

// The page has to be able to SAY which of the three reasons an operation is
// off for — the whole point of the request. That means the read model, not
// just the resolver, has to carry state + inherited separately.
func TestAPIAccountDetailShowsInheritVersusOverride(t *testing.T) {
	svc := newConnectorsSvcForAPI(t, []connector.Module{historyModule("slack")})
	h := &Handler{connectors: svc}
	row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SaveAccount(t.Context(), row.ID, "u-admin", "U1", "yoga.setiawan", "xoxp-test"); err != nil {
		t.Fatalf("save account: %v", err)
	}
	accs, _ := svc.ListAccounts(t.Context(), row.ID)
	accID := accs[0].ID

	get := func(t *testing.T) accountDetailJSON {
		t.Helper()
		req := adminReq(t, http.MethodGet, "/manager/api/connectors/slack/"+row.ID+"/accounts/"+accID, nil)
		req.SetPathValue("key", "slack")
		req.SetPathValue("id", row.ID)
		req.SetPathValue("accountID", accID)
		rec := httptest.NewRecorder()
		h.apiAccountDetail(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var out accountDetailJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	opNamed := func(d accountDetailJSON, key string) accountOpJSON {
		for _, op := range d.Ops {
			if op.Key == key {
				return op
			}
		}
		t.Fatalf("op %q missing from %+v", key, d.Ops)
		return accountOpJSON{}
	}

	t.Run("inheriting an instance-on operation", func(t *testing.T) {
		op := opNamed(get(t), "send")
		if op.State != connectors.AccountOpInherit || !op.Enabled || !op.Inherited {
			t.Errorf("want inherit/on/on, got state=%q enabled=%v inherited=%v", op.State, op.Enabled, op.Inherited)
		}
	})

	t.Run("inheriting an instance-off operation is not an override", func(t *testing.T) {
		if err := svc.SetOperationEnabled(t.Context(), row.ID, "send", false); err != nil {
			t.Fatalf("disable op: %v", err)
		}
		op := opNamed(get(t), "send")
		// This is the pair that used to be indistinguishable.
		if op.State != connectors.AccountOpInherit {
			t.Errorf("state = %q, want inherit — the account has said nothing", op.State)
		}
		if op.Enabled || op.Inherited {
			t.Errorf("want off via the instance, got enabled=%v inherited=%v", op.Enabled, op.Inherited)
		}
	})

	t.Run("an override reports itself, and what it overrides", func(t *testing.T) {
		if err := svc.SetAccountOpOverride(t.Context(), accID, "send", connectors.AccountOpOn); err != nil {
			t.Fatalf("override: %v", err)
		}
		op := opNamed(get(t), "send")
		if op.State != connectors.AccountOpOn || !op.Enabled {
			t.Errorf("want on/enabled, got state=%q enabled=%v", op.State, op.Enabled)
		}
		if op.Inherited {
			t.Error("inherited should still report the instance's off, so the UI can say what clearing it does")
		}
	})

	t.Run("the POST round-trips all three states", func(t *testing.T) {
		post := func(state string) int {
			body, _ := json.Marshal(map[string]string{"state": state})
			req := adminReq(t, http.MethodPost,
				"/manager/api/connectors/slack/"+row.ID+"/accounts/"+accID+"/ops/send", body)
			req.SetPathValue("key", "slack")
			req.SetPathValue("id", row.ID)
			req.SetPathValue("accountID", accID)
			req.SetPathValue("opKey", "send")
			rec := httptest.NewRecorder()
			h.apiSetAccountOpOverride(rec, req)
			return rec.Code
		}
		for _, state := range []string{connectors.AccountOpOff, connectors.AccountOpOn, connectors.AccountOpInherit} {
			if code := post(state); code != http.StatusOK {
				t.Fatalf("POST state=%q = %d, want 200", state, code)
			}
			if got := opNamed(get(t), "send").State; got != state {
				t.Errorf("after POST %q the read model says %q", state, got)
			}
		}
		if code := post("maybe"); code != http.StatusBadRequest {
			t.Errorf("an unknown state should be 400, got %d", code)
		}
	})
}
