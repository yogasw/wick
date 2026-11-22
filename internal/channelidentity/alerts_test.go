package channelidentity

import (
	"context"
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestParseAlertTargets(t *testing.T) {
	cases := map[string]AlertTargets{
		"":                     {All: true},
		" all ":                {All: true},
		"off":                  {Off: true},
		"push,off,slack":       {Off: true},
		"push":                 {Push: true},
		"slack, slack:acme":    {Doors: []string{"slack", "slack:acme"}},
		"push,slack:acme#C01X": {Push: true, Rooms: []AlertRoom{{ChannelType: "slack", InstanceKey: "slack:acme", RoomID: "C01X"}}},
		"slack:acme#,#C01X":    {},
	}
	for raw, want := range cases {
		if got := ParseAlertTargets(raw); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseAlertTargets(%q) = %+v, want %+v", raw, got, want)
		}
	}
}

type fakeRooms struct{ posts []string }

func (f *fakeRooms) PostToRoom(_ context.Context, ct, ik, room, _ string) error {
	f.posts = append(f.posts, ct+"|"+ik+"|"+room)
	return nil
}

func alertSetup(t *testing.T, targets string) (*Notifier, *fakePush, *fakeChannels, *fakeRooms) {
	t.Helper()
	n, st, push, chs, admins := newNotifier(t)
	rooms := &fakeRooms{}
	n.Rooms = rooms
	n.AlertTargets = func() string { return targets }
	admins.admins = []*entity.User{
		{ID: "admin-1", Email: "a1@example.com", Role: entity.RoleAdmin, Approved: true},
		{ID: "admin-2", Email: "a2@example.com", Role: entity.RoleAdmin, Approved: true},
	}
	ctx := context.Background()
	for _, l := range []entity.UserChannelIdentity{
		ident("admin-1", "slack:acme", "UADMIN1"),
		ident("admin-2", "slack:other", "UADMIN2"),
	} {
		if err := st.Link(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	return n, push, chs, rooms
}

func TestAlertTargets_RoomOnlySendsOnce(t *testing.T) {
	n, push, chs, rooms := alertSetup(t, "slack:acme#C01TESTCHAN")
	n.NotifyAdminsNewUser(context.Background(), &entity.User{ID: "new", Email: "new@example.com"}, "google")
	if len(push.sent) != 0 || len(chs.sent) != 0 {
		t.Fatalf("room-only must not push or DM: push=%v dm=%+v", push.sent, chs.sent)
	}
	if !reflect.DeepEqual(rooms.posts, []string{"slack|slack:acme|C01TESTCHAN"}) {
		t.Fatalf("room posts = %v", rooms.posts)
	}
}

func TestAlertTargets_DMOnlyThroughListedInstance(t *testing.T) {
	n, push, chs, rooms := alertSetup(t, "slack:acme")
	n.NotifyAdminsMergeNeeded(context.Background(), "x", "Someone", "telegram")
	if len(push.sent) != 0 || len(rooms.posts) != 0 {
		t.Fatalf("unexpected push=%v rooms=%v", push.sent, rooms.posts)
	}
	if len(chs.sent) != 1 || chs.sent[0].external != "UADMIN1" {
		t.Fatalf("DMs = %+v, want only UADMIN1 via slack:acme", chs.sent)
	}
}

func TestAlertTargets_OffSilencesAdminsNotUser(t *testing.T) {
	n, push, chs, rooms := alertSetup(t, "off")
	n.NotifyUserApproved(context.Background(), &entity.User{ID: "admin-1", Email: "a1@example.com"})
	// admin-1 is the approved user here: the user-facing notice still goes out.
	if !reflect.DeepEqual(push.sent, []string{"admin-1"}) {
		t.Fatalf("push = %v, want only the user's own notice", push.sent)
	}
	if len(chs.sent) != 1 || len(rooms.posts) != 0 {
		t.Fatalf("dm=%+v rooms=%v", chs.sent, rooms.posts)
	}
}

func TestAlertTargets_PushOnly(t *testing.T) {
	n, push, chs, _ := alertSetup(t, "push")
	n.NotifyAdminsNewUser(context.Background(), &entity.User{ID: "new", Email: "new@example.com"}, "slack")
	if len(push.sent) != 2 || len(chs.sent) != 0 {
		t.Fatalf("push=%v dm=%+v", push.sent, chs.sent)
	}
}
