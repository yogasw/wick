package agents

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	wfprovider "github.com/yogasw/wick/internal/agents/workflow/provider"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// stubProviderTags stands in for the tag store: only the listed
// "type/name" keys are tagged, and only the ones in allow pass for a
// non-admin. Untagged instances pass for everyone, as in the real rule.
func stubProviderTags(t *testing.T, tagged, allow []string) {
	t.Helper()
	prev := providerAccessTagAllows
	t.Cleanup(func() { providerAccessTagAllows = prev })
	providerAccessTagAllows = func(_ context.Context, _ *entity.User, typ provider.Type, name string) bool {
		key := string(typ) + "/" + name
		isTagged := false
		for _, k := range tagged {
			isTagged = isTagged || k == key
		}
		if !isTagged {
			return true
		}
		for _, k := range allow {
			if k == key {
				return true
			}
		}
		return false
	}
}

func ctxWithUser(u *entity.User) *tool.Ctx {
	r := httptest.NewRequest("GET", "/tools/agents/workflows/api/catalog", nil)
	if u != nil {
		r = r.WithContext(login.WithUser(r.Context(), u, nil))
	}
	return tool.NewCtx(httptest.NewRecorder(), r, nil, tool.Tool{}, nil, nil)
}

func names(rows []wfprovider.Info) string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Type+"/"+r.Name)
	}
	return strings.Join(out, ",")
}

// The workflow editor's provider list obeys the same ACCESS tags as every
// other picker: admins see all, a non-admin loses the instances tagged
// away from them, untagged ones stay open.
func TestWorkflowProviderChoicesFiltersByAccessTag(t *testing.T) {
	stubProviderTags(t, []string{"claude/ent", "codex/team"}, []string{"codex/team"})
	rows := []wfprovider.Info{
		{Name: "claude", Type: "claude", IsDefault: true},
		{Name: "ent", Type: "claude"},
		{Name: "team", Type: "codex"},
	}
	cases := []struct {
		name string
		user *entity.User
		want string
	}{
		{"admin sees all", &entity.User{ID: "a", Role: entity.RoleAdmin, Approved: true}, "claude/claude,claude/ent,codex/team"},
		{"non-admin loses tagged-away instance", &entity.User{ID: "u", Role: entity.RoleUser, Approved: true}, "claude/claude,codex/team"},
		{"unapproved sees nothing", &entity.User{ID: "p", Role: entity.RoleUser}, ""},
		{"anonymous sees nothing", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := names(workflowProviderChoices(ctxWithUser(tc.user), rows)); got != tc.want {
				t.Errorf("choices = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkflowProviderChoicesUntaggedOpenToAll(t *testing.T) {
	stubProviderTags(t, nil, nil)
	rows := []wfprovider.Info{{Name: "a", Type: "claude"}, {Name: "b", Type: "gemini"}}
	u := &entity.User{ID: "u", Role: entity.RoleUser, Approved: true}
	if got := names(workflowProviderChoices(ctxWithUser(u), rows)); got != "claude/a,gemini/b" {
		t.Errorf("choices = %q, want both", got)
	}
}

// The run-time gate asks about the workflow OWNER.
func TestWorkflowProviderAccessOwnerRule(t *testing.T) {
	stubProviderTags(t, []string{"claude/ent"}, nil)
	users := map[string]*entity.User{
		"admin": {ID: "admin", Role: entity.RoleAdmin, Approved: true},
		"user":  {ID: "user", Role: entity.RoleUser, Approved: true},
	}
	prev := lookupWorkflowOwner
	t.Cleanup(func() { lookupWorkflowOwner = prev })
	lookupWorkflowOwner = func(_ context.Context, id string) (*entity.User, error) {
		if u, ok := users[id]; ok {
			return u, nil
		}
		return nil, errors.New("not found")
	}
	ctx := context.Background()
	if err := workflowProviderAccess(ctx, "", "claude", "ent"); err != nil {
		t.Errorf("owner-less workflow refused: %v", err)
	}
	if err := workflowProviderAccess(ctx, "admin", "claude", "ent"); err != nil {
		t.Errorf("admin owner refused: %v", err)
	}
	if err := workflowProviderAccess(ctx, "user", "claude", "open"); err != nil {
		t.Errorf("untagged provider refused: %v", err)
	}
	err := workflowProviderAccess(ctx, "user", "claude", "ent")
	if err == nil || !strings.Contains(err.Error(), "owner has no access to provider claude/ent") {
		t.Errorf("tagged-away provider: err = %v, want owner has no access", err)
	}
	if err := workflowProviderAccess(ctx, "gone", "claude", "open"); err == nil {
		t.Error("deleted owner allowed, want fail closed")
	}
}
