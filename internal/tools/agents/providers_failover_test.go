package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

// failoverWorld: "owner" owns claude/mine and may pick codex/waba and
// omp/wick but not codex/secret; admins see everything.
func failoverWorld(t *testing.T) map[string]provider.Instance {
	t.Helper()
	store := map[string]provider.Instance{
		"claude/mine":    {Type: provider.TypeClaude, Name: "mine"},
		"claude/locked":  {Type: provider.TypeClaude, Name: "locked", OwnerPerms: map[string]bool{ownerPermFailover: false}},
		"codex/waba":     {Type: provider.TypeCodex, Name: "waba"},
		"codex/secret":   {Type: provider.TypeCodex, Name: "secret"},
		"omp/wick":       {Type: provider.TypeOMP, Name: "wick"},
		"claude/sibling": {Type: provider.TypeClaude, Name: "sibling"},
	}
	stubProviderInstances(t, store)
	prevList, prevOwned, prevAccess := listProviderInstancesFn, providerOwnedBy, providerAccessTagAllows
	t.Cleanup(func() {
		listProviderInstancesFn, providerOwnedBy, providerAccessTagAllows = prevList, prevOwned, prevAccess
	})
	listProviderInstancesFn = func() ([]provider.Instance, error) {
		out := make([]provider.Instance, 0, len(store))
		for _, ins := range store {
			out = append(out, ins)
		}
		return out, nil
	}
	providerOwnedBy = func(_ context.Context, u *entity.User, _ provider.Type, name string) bool {
		return u.ID == "owner" && (name == "mine" || name == "locked")
	}
	providerAccessTagAllows = func(_ context.Context, _ *entity.User, _ provider.Type, name string) bool {
		return name != "secret"
	}
	return store
}

func decodeFailover(t *testing.T, body string) FailoverResponse {
	t.Helper()
	var r FailoverResponse
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return r
}

var (
	failoverAdmin = &entity.User{ID: "admin", Role: entity.RoleAdmin, Approved: true}
	failoverOwner = &entity.User{ID: "owner", Role: entity.RoleUser, Approved: true}
	failoverOther = &entity.User{ID: "other", Role: entity.RoleUser, Approved: true}
)

func TestProviderFailoverGet(t *testing.T) {
	store := failoverWorld(t)
	mine := map[string]string{"type": "claude", "name": "mine"}

	w, c := userCtx(t, failoverOwner, "/", mine)
	providerFailover(c)
	if w.Code != http.StatusOK {
		t.Fatalf("owner: status=%d body=%s", w.Code, w.Body.String())
	}
	r := decodeFailover(t, w.Body.String())
	want := FailoverConfigDTO{Triggers: []string{"limit", "auth", "unavailable"}, Chain: []FailoverTargetDTO{}, RecheckMinutes: 10, ReturnHome: true}
	if !reflect.DeepEqual(r.Config, want) || !r.CanEdit || len(r.Errors) != 0 || len(r.Warnings) != 0 {
		t.Fatalf("unconfigured instance = %+v", r)
	}

	// Someone who does not manage the instance does not learn it exists.
	w, c = userCtx(t, failoverOther, "/", mine)
	providerFailover(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("other: status=%d", w.Code)
	}
	w, c = userCtx(t, failoverAdmin, "/", map[string]string{"type": "claude", "name": "gone"})
	providerFailover(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing instance: status=%d", w.Code)
	}

	// The owner may read a chain they may not edit.
	w, c = userCtx(t, failoverOwner, "/", map[string]string{"type": "claude", "name": "locked"})
	providerFailover(c)
	if r := decodeFailover(t, w.Body.String()); w.Code != http.StatusOK || r.CanEdit {
		t.Fatalf("locked: status=%d can_edit=%v", w.Code, r.CanEdit)
	}

	// A stored chain whose target has gone since reads back with errors
	// (the UI marks it) and its cross-type warning.
	ins := store["claude/mine"]
	ins.Failover = &provider.FailoverConfig{Enabled: true, Triggers: []string{"limit"}, RecheckMinutes: 10,
		Chain: []provider.FailoverTarget{{Instance: "codex/removed"}}}
	store["claude/mine"] = ins
	w, c = userCtx(t, failoverAdmin, "/", mine)
	providerFailover(c)
	r = decodeFailover(t, w.Body.String())
	if w.Code != http.StatusOK || len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "codex/removed") {
		t.Fatalf("stale chain: status=%d %+v", w.Code, r)
	}
	if len(r.Warnings) != 1 || r.Warnings[0].Message != "history does not carry over to codex" {
		t.Fatalf("stale chain warnings: %+v", r.Warnings)
	}
}

func TestSaveProviderFailover(t *testing.T) {
	store := failoverWorld(t)
	mine := map[string]string{"type": "claude", "name": "mine"}
	post := func(u *entity.User, path map[string]string, body string) (int, FailoverResponse, string) {
		w, c := postCtx(t, u, "/", body, path)
		saveProviderFailover(c)
		var r FailoverResponse
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return w.Code, r, w.Body.String()
	}

	// Valid, cross-type: saved, normalized, warned.
	code, r, body := post(failoverOwner, mine, `{"enabled":true,"triggers":["Limit","auth"],
		"chain":[{"instance":"codex/waba","model_id":"acc@gpt-5"},{"instance":"claude/sibling"}],
		"recheck_minutes":5,"return_home":true}`)
	if code != http.StatusOK {
		t.Fatalf("valid: status=%d body=%s", code, body)
	}
	got := store["claude/mine"].Failover
	want := &provider.FailoverConfig{Enabled: true, Triggers: []string{"limit", "auth"}, RecheckMinutes: 5, ReturnHome: true,
		Chain: []provider.FailoverTarget{{Instance: "codex/waba", ModelID: "acc@gpt-5"}, {Instance: "claude/sibling"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored = %+v, want %+v", got, want)
	}
	if len(r.Warnings) != 1 || r.Warnings[0].Instance != "codex/waba" || len(r.Errors) != 0 {
		t.Fatalf("valid response = %+v", r)
	}

	// Invalid: nothing stored, every error returned.
	code, r, body = post(failoverOwner, mine, `{"enabled":true,"triggers":["limit"],
		"chain":[{"instance":"claude/mine"},{"instance":"omp/wick"},{"instance":"omp/wick"}],"recheck_minutes":61}`)
	if code != http.StatusBadRequest || len(r.Errors) != 3 {
		t.Fatalf("invalid: status=%d body=%s", code, body)
	}
	if !reflect.DeepEqual(store["claude/mine"].Failover, want) {
		t.Fatal("an invalid save must not touch the stored chain")
	}

	// A target the owner may not pick reads as unknown, once.
	code, r, body = post(failoverOwner, mine, `{"enabled":true,"triggers":["limit"],
		"chain":[{"instance":"codex/secret"}],"recheck_minutes":10}`)
	if code != http.StatusBadRequest || !reflect.DeepEqual(r.Errors, []string{"unknown provider instance codex/secret"}) {
		t.Fatalf("no access: status=%d body=%s", code, body)
	}

	// An admin may chain it; the owner may then keep it, not add more.
	if code, _, body = post(failoverAdmin, mine, `{"enabled":true,"triggers":["limit"],
		"chain":[{"instance":"codex/secret"}],"recheck_minutes":10}`); code != http.StatusOK {
		t.Fatalf("admin: status=%d body=%s", code, body)
	}
	if code, _, body = post(failoverOwner, mine, `{"enabled":false,"triggers":["limit"],
		"chain":[{"instance":"codex/secret"},{"instance":"omp/wick"}],"recheck_minutes":10}`); code != http.StatusOK {
		t.Fatalf("owner keeping an admin's target: status=%d body=%s", code, body)
	}
	if fo := store["claude/mine"].Failover; fo.Enabled || len(fo.Chain) != 2 {
		t.Fatalf("stored after keep = %+v", fo)
	}

	// Refusals: not the owner, owner without the permission, bad JSON.
	if code, _, _ = post(failoverOther, mine, `{"enabled":false,"recheck_minutes":10}`); code != http.StatusForbidden {
		t.Fatalf("other: status=%d", code)
	}
	if code, _, _ = post(failoverOwner, map[string]string{"type": "claude", "name": "locked"}, `{"enabled":false,"recheck_minutes":10}`); code != http.StatusForbidden {
		t.Fatalf("locked: status=%d", code)
	}
	if store["claude/locked"].Failover != nil {
		t.Fatal("a refused save must not be stored")
	}
	if code, _, _ = post(failoverOwner, mine, `{nope`); code != http.StatusBadRequest {
		t.Fatalf("bad json: status=%d", code)
	}

	// A bare tag and its canonical form are the same target.
	code, r, body = post(failoverOwner, mine, `{"enabled":false,"triggers":["limit"],
		"chain":[{"instance":"omp"},{"instance":"omp/omp"}]}`)
	if code != http.StatusBadRequest || !reflect.DeepEqual(r.Errors, []string{"omp/omp is listed more than once"}) {
		t.Fatalf("bare + canonical duplicate: status=%d body=%s", code, body)
	}
}

// A target the owner may not pick yields ONE generic error, whether it
// is missing, disabled or fine — nothing tells them apart.
func TestSaveProviderFailoverDeniedTargetIsOpaque(t *testing.T) {
	store := failoverWorld(t)
	sec := store["codex/secret"]
	sec.Disabled = true
	store["codex/secret"] = sec
	mine := map[string]string{"type": "claude", "name": "mine"}
	for _, tag := range []string{"codex/secret", "codex/nosecret_gone"} {
		// "nosecret_gone" does not exist; the stub grants access to any
		// name but "secret", so it fails on existence alone.
		w, c := postCtx(t, failoverOwner, "/", `{"enabled":true,"triggers":["limit"],"chain":[{"instance":"`+tag+`"}]}`, mine)
		saveProviderFailover(c)
		r := decodeFailover(t, w.Body.String())
		if w.Code != http.StatusBadRequest || !reflect.DeepEqual(r.Errors, []string{"unknown provider instance " + tag}) || len(r.Warnings) != 1 {
			t.Fatalf("%s: status=%d body=%s", tag, w.Code, w.Body.String())
		}
	}
}

// A stored target whose instance was deleted is no longer "already
// stored": re-created under the same name it must pass the access check.
func TestSaveProviderFailoverStoredTargetGoneIsChecked(t *testing.T) {
	store := failoverWorld(t)
	mine := map[string]string{"type": "claude", "name": "mine"}
	ins := store["claude/mine"]
	ins.Failover = &provider.FailoverConfig{Triggers: []string{"limit"}, RecheckMinutes: 10,
		Chain: []provider.FailoverTarget{{Instance: "codex/secret"}}}
	store["claude/mine"] = ins
	body := `{"enabled":false,"triggers":["limit"],"chain":[{"instance":"codex/secret"}]}`

	w, c := postCtx(t, failoverOwner, "/", body, mine)
	saveProviderFailover(c)
	if w.Code != http.StatusOK {
		t.Fatalf("stored + present: status=%d body=%s", w.Code, w.Body.String())
	}
	delete(store, "codex/secret")
	w, c = postCtx(t, failoverOwner, "/", body, mine)
	saveProviderFailover(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("stored but gone: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestFailoverSummaryDTO(t *testing.T) {
	if failoverSummaryDTO(provider.Instance{}) != nil {
		t.Fatal("unconfigured instance must have no summary")
	}
	got := failoverSummaryDTO(provider.Instance{Failover: &provider.FailoverConfig{Enabled: true,
		Chain: []provider.FailoverTarget{{Instance: "codex/waba", ModelID: "x"}, {Instance: "omp/wick"}}}})
	if !got.Enabled || !reflect.DeepEqual(got.Targets, []string{"codex/waba", "omp/wick"}) {
		t.Fatalf("summary = %+v", got)
	}
	b, _ := json.Marshal(providerStatusDTO(provider.Status{Instance: provider.Instance{Type: provider.TypeClaude, Name: "c"}}, nil))
	if strings.Contains(string(b), `"failover"`) || strings.Contains(string(b), `"health"`) {
		t.Fatalf("unconfigured list row must omit failover/health: %s", b)
	}
}
