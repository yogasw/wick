package wickmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/admin"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
)

type fakePeople struct {
	PeopleSource
	signup admin.SignupPatch
}

func (f *fakePeople) ManageSetSignup(_ context.Context, p admin.SignupPatch) (admin.SignupSettings, error) {
	f.signup = p
	return admin.SignupSettings{}, nil
}

func peopleCtx(u *entity.User, in map[string]string) *connector.Ctx {
	ctx := context.Background()
	if u != nil {
		ctx = login.WithUser(ctx, u, nil)
	}
	return connector.NewCtx(ctx, "", nil, in, nil, nil, nil)
}

func TestPeopleOpsAdminOnly(t *testing.T) {
	src := &fakePeople{}
	h := newHandlers(Deps{People: func() PeopleSource { return src }})
	plain := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	if _, err := h.signupSet(peopleCtx(plain, map[string]string{"auto_register": "true"})); !errors.Is(err, errAccessDenied) {
		t.Fatalf("non-admin: err = %v, want access denied", err)
	}
	if _, err := h.signupSet(peopleCtx(nil, map[string]string{"auto_register": "true"})); !errors.Is(err, errNotAuthenticated) {
		t.Fatalf("anonymous: err = %v", err)
	}
}

func TestSignupSetPatch(t *testing.T) {
	src := &fakePeople{}
	h := newHandlers(Deps{People: func() PeopleSource { return src }})
	boss := &entity.User{ID: "a1", Role: entity.RoleAdmin, Approved: true}

	if _, err := h.signupSet(peopleCtx(boss, map[string]string{"auto_register": "keep"})); err == nil {
		t.Fatal("empty patch accepted")
	}
	if _, err := h.signupSet(peopleCtx(boss, map[string]string{
		"auto_register": "false", "auto_approve_domains": "acme.test", "allowed_domains": "-",
	})); err != nil {
		t.Fatal(err)
	}
	p := src.signup
	if p.AutoRegister == nil || *p.AutoRegister || p.AutoApproveDomains == nil || *p.AutoApproveDomains != "acme.test" ||
		p.AllowedDomains == nil || *p.AllowedDomains != "" {
		t.Fatalf("patch = %+v", p)
	}
}

func TestPeopleUnavailable(t *testing.T) {
	h := newHandlers(Deps{})
	boss := &entity.User{ID: "a1", Role: entity.RoleAdmin, Approved: true}
	if _, err := h.tagList(peopleCtx(boss, nil)); !errors.Is(err, errPeopleUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestDescribeTargets(t *testing.T) {
	got := describeTargets("push,slack:acme#C01TESTCHAN")
	if len(got) != 2 {
		t.Fatalf("describe = %v", got)
	}
}
