package channelidentity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

type fakePush struct {
	sent []string // userIDs
	err  error
}

func (f *fakePush) SendToUser(_ context.Context, userID, _, _, _ string) (int, error) {
	f.sent = append(f.sent, userID)
	return 1, f.err
}

type sentDM struct {
	channelType string
	instanceKey string
	external    string
	text        string
}

type fakeChannels struct {
	sent []sentDM
	err  error
}

func (f *fakeChannels) SendDirect(_ context.Context, ct, ik, ext, text string) error {
	f.sent = append(f.sent, sentDM{ct, ik, ext, text})
	return f.err
}

type fakeAdmins struct {
	admins []*entity.User
	err    error
}

func (f *fakeAdmins) ListApprovedAdmins(context.Context) ([]*entity.User, error) {
	return f.admins, f.err
}

func newNotifier(t *testing.T) (*Notifier, *Store, *fakePush, *fakeChannels, *fakeAdmins) {
	t.Helper()
	st := NewStore(newDB(t))
	push := &fakePush{}
	chs := &fakeChannels{}
	admins := &fakeAdmins{}
	return &Notifier{
		Store: st, Push: push, Channels: chs, Admins: admins,
		AppURL: func() string { return "https://wick.example.com/" },
	}, st, push, chs, admins
}

// TestNotifyAdminsNewUser_ReachesEveryAdminDoor is event 1: a channel
// registration must reach admins on push AND on their own chat connections.
func TestNotifyAdminsNewUser_ReachesEveryAdminDoor(t *testing.T) {
	n, st, push, chs, admins := newNotifier(t)
	ctx := context.Background()

	admins.admins = []*entity.User{
		{ID: "admin-1", Email: "a1@example.com", Name: "A1", Role: entity.RoleAdmin, Approved: true},
		{ID: "admin-2", Email: "a2@example.com", Name: "A2", Role: entity.RoleAdmin, Approved: true},
	}
	// admin-1 also has Slack; admin-2 only has push.
	if err := st.Link(ctx, ident("admin-1", "slack:acme", "UADMIN1")); err != nil {
		t.Fatalf("link: %v", err)
	}

	n.NotifyAdminsNewUser(ctx, &entity.User{
		ID: "new-1", Email: "new@example.com", Name: "New Person",
	}, "slack")

	if len(push.sent) != 2 {
		t.Fatalf("push sent to %v, want both admins", push.sent)
	}
	if len(chs.sent) != 1 || chs.sent[0].external != "UADMIN1" {
		t.Fatalf("channel DMs = %+v, want one to UADMIN1", chs.sent)
	}
	dm := chs.sent[0]
	if dm.instanceKey != "slack:acme" {
		t.Errorf("instance = %q; a DM must go out through the instance the identity belongs to", dm.instanceKey)
	}
	if !strings.Contains(dm.text, "new@example.com") {
		t.Errorf("DM should name who registered: %q", dm.text)
	}
	if !strings.Contains(dm.text, "/admin/users") {
		t.Errorf("DM should link where to act: %q", dm.text)
	}
}

// TestNotifyAdminsNewUser_SkipsSelf: a user who somehow triggers their own
// registration notice should not be asked to approve themselves.
func TestNotifyAdminsNewUser_SkipsSelf(t *testing.T) {
	n, _, push, _, admins := newNotifier(t)
	admins.admins = []*entity.User{
		{ID: "same", Email: "s@example.com", Role: entity.RoleAdmin, Approved: true},
	}
	n.NotifyAdminsNewUser(context.Background(), &entity.User{ID: "same"}, "slack")
	if len(push.sent) != 0 {
		t.Fatalf("notified self: %v", push.sent)
	}
}

// TestNotifyAdminsNewUser_SurvivesDeliveryFailure: the account already exists,
// so a failed notice must not panic or short-circuit the remaining admins.
func TestNotifyAdminsNewUser_SurvivesDeliveryFailure(t *testing.T) {
	n, st, push, chs, admins := newNotifier(t)
	ctx := context.Background()
	push.err = errors.New("push endpoint gone")
	chs.err = errors.New("missing_scope")

	admins.admins = []*entity.User{
		{ID: "admin-1", Email: "a1@example.com", Role: entity.RoleAdmin, Approved: true},
		{ID: "admin-2", Email: "a2@example.com", Role: entity.RoleAdmin, Approved: true},
	}
	if err := st.Link(ctx, ident("admin-1", "slack:acme", "UADMIN1")); err != nil {
		t.Fatalf("link: %v", err)
	}

	n.NotifyAdminsNewUser(ctx, &entity.User{ID: "new-1", Email: "n@example.com"}, "slack")

	// Both were still attempted despite each transport erroring.
	if len(push.sent) != 2 {
		t.Fatalf("push attempts = %v, want 2 despite errors", push.sent)
	}
	if len(chs.sent) != 1 {
		t.Fatalf("channel attempts = %d, want 1 despite error", len(chs.sent))
	}
}

// TestNotifyAdminsNewUser_NoAdmins must not panic. Nobody being reachable is a
// real state on a fresh install, and it is logged rather than swallowed.
func TestNotifyAdminsNewUser_NoAdmins(t *testing.T) {
	n, _, push, chs, _ := newNotifier(t)
	n.NotifyAdminsNewUser(context.Background(), &entity.User{ID: "x"}, "slack")
	if len(push.sent) != 0 || len(chs.sent) != 0 {
		t.Fatal("sent something with no admins configured")
	}
}

// TestNotifyUserApproved_UsesTheChannelTheyCameFrom is event 2. The channel is
// where the person is waiting, so a web-only notice would be the one place they
// are not looking.
func TestNotifyUserApproved_UsesTheChannelTheyCameFrom(t *testing.T) {
	n, st, push, chs, _ := newNotifier(t)
	ctx := context.Background()

	if err := st.Link(ctx, ident("u1", "slack:acme", "U123")); err != nil {
		t.Fatalf("link: %v", err)
	}
	n.NotifyUserApproved(ctx, &entity.User{ID: "u1", Email: "ada@example.com", Name: "Ada"})

	if len(push.sent) != 1 || push.sent[0] != "u1" {
		t.Fatalf("push = %v, want [u1]", push.sent)
	}
	if len(chs.sent) != 1 || chs.sent[0].external != "U123" {
		t.Fatalf("channel DMs = %+v, want one to U123", chs.sent)
	}
	if !strings.Contains(strings.ToLower(chs.sent[0].text), "approved") {
		t.Errorf("message should say the account is approved: %q", chs.sent[0].text)
	}
}

// TestNotifyUserApproved_RespectsPause is what makes the pause button real: a
// paused connection must be skipped at SEND time, not merely greyed in the UI.
func TestNotifyUserApproved_RespectsPause(t *testing.T) {
	n, st, _, chs, _ := newNotifier(t)
	ctx := context.Background()

	if err := st.Link(ctx, ident("u1", "slack:acme", "U123")); err != nil {
		t.Fatalf("link: %v", err)
	}
	rows, _ := st.ListForUser(ctx, "u1")
	if err := st.SetPaused(ctx, "u1", rows[0].ID, true); err != nil {
		t.Fatalf("pause: %v", err)
	}

	n.NotifyUserApproved(ctx, &entity.User{ID: "u1"})
	if len(chs.sent) != 0 {
		t.Fatalf("delivered to a paused connection: %+v", chs.sent)
	}
}

// TestNotifier_NoAppURLOmitsLink: a half-formed link is worse than none.
func TestNotifier_NoAppURLOmitsLink(t *testing.T) {
	n, st, _, chs, _ := newNotifier(t)
	n.AppURL = func() string { return "" }
	ctx := context.Background()

	if err := st.Link(ctx, ident("u1", "slack:acme", "U123")); err != nil {
		t.Fatalf("link: %v", err)
	}
	n.NotifyUserApproved(ctx, &entity.User{ID: "u1"})
	if len(chs.sent) != 1 {
		t.Fatalf("expected one DM, got %d", len(chs.sent))
	}
	if strings.Contains(chs.sent[0].text, "http") {
		t.Errorf("emitted a link with no app URL configured: %q", chs.sent[0].text)
	}
}

// TestNotifier_NilSafe: a partially wired notifier must not panic.
func TestNotifier_NilSafe(t *testing.T) {
	var n *Notifier
	n.NotifyAdminsNewUser(context.Background(), &entity.User{ID: "x"}, "slack")
	n.NotifyUserApproved(context.Background(), &entity.User{ID: "x"})

	bare := &Notifier{}
	bare.NotifyAdminsNewUser(context.Background(), &entity.User{ID: "x"}, "slack")
	bare.NotifyUserApproved(context.Background(), &entity.User{ID: "x"})
}
