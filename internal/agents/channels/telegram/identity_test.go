package telegram

import (
	"context"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// fakeUsers is the smallest UserResolver that lets the gate be exercised.
type fakeUsers struct {
	byEmail      map[string]string
	byExternal   map[string]string
	pending      map[string]bool
	denyAgents   map[string]bool
	autoRegister bool
	registered   []string
	recorded     map[string]string
}

func (f *fakeUsers) FindByChannelIdentity(_ context.Context, _, _, ext string) (string, bool) {
	id, ok := f.byExternal[ext]
	return id, ok
}
func (f *fakeUsers) FindByEmail(_ context.Context, email string) (string, bool) {
	id, ok := f.byEmail[email]
	return id, ok
}
func (f *fakeUsers) RegisterFromChannel(_ context.Context, email, _, _ string) (string, error) {
	f.registered = append(f.registered, email)
	if f.byEmail == nil {
		f.byEmail = map[string]string{}
	}
	f.byEmail[email] = "new-" + email
	return "new-" + email, nil
}
func (f *fakeUsers) IsApproved(_ context.Context, id string) bool   { return !f.pending[id] }
func (f *fakeUsers) AutoRegisterEnabled(context.Context) bool       { return f.autoRegister }
func (f *fakeUsers) CanUseAgents(_ context.Context, id string) bool { return !f.denyAgents[id] }
func (f *fakeUsers) RecordIdentity(_ context.Context, wickID, ext, _, _ string) {
	if f.recorded == nil {
		f.recorded = map[string]string{}
	}
	f.recorded[ext] = wickID
}

func msgFrom(id int64, name string) *tgbotapi.Message {
	return &tgbotapi.Message{From: &tgbotapi.User{ID: id, FirstName: name}}
}

func chanWith(users agentchannels.UserResolver) *Channel {
	return &Channel{users: users, ownerUserID: "owner-1"}
}

// Telegram reports no email, so the numeric id becomes a reserved-domain
// stand-in. It must be stable and derived from the id — that is what lets an
// admin merge the placeholder into a real account later.
func TestSenderIdentityUsesSyntheticEmail(t *testing.T) {
	got, ok := senderIdentityFor(msgFrom(8812, "Yoga"))
	if !ok {
		t.Fatal("expected an identity")
	}
	if got.ExternalUserID != "8812" {
		t.Errorf("ExternalUserID = %q, want 8812", got.ExternalUserID)
	}
	if got.Email != "8812@telegram.local" {
		t.Errorf("Email = %q, want the reserved-domain stand-in", got.Email)
	}
}

// The whole reason this exists: an unknown sender must not silently run as
// the channel owner.
func TestCheckSenderIdentityRefusesUnknown(t *testing.T) {
	c := chanWith(&fakeUsers{autoRegister: false})
	reply := c.checkSenderIdentity(msgFrom(8812, "Stranger"))
	if reply == "" {
		t.Fatal("an unknown sender was allowed through")
	}
	if !strings.Contains(reply, "wick account") {
		t.Errorf("reply should say what to do: %q", reply)
	}
}

// Auto-register creates the account but it starts PENDING, so the sender is
// told to wait rather than getting in.
func TestCheckSenderIdentityAutoRegisterStillGated(t *testing.T) {
	f := &fakeUsers{autoRegister: true, pending: map[string]bool{"new-8812@telegram.local": true}}
	c := chanWith(f)

	reply := c.checkSenderIdentity(msgFrom(8812, "Yoga"))
	if reply == "" {
		t.Fatal("a freshly auto-registered account was allowed straight in")
	}
	if !strings.Contains(strings.ToLower(reply), "approv") {
		t.Errorf("reply should point at approval: %q", reply)
	}
	if len(f.registered) != 1 || f.registered[0] != "8812@telegram.local" {
		t.Errorf("registered = %v, want the synthetic address once", f.registered)
	}
}

// Approved but not granted the Agents tool is a different fix from pending,
// so it has to read differently.
func TestCheckSenderIdentityToolGate(t *testing.T) {
	c := chanWith(&fakeUsers{
		byExternal: map[string]string{"8812": "u-yoga"},
		denyAgents: map[string]bool{"u-yoga": true},
	})
	reply := c.checkSenderIdentity(msgFrom(8812, "Yoga"))
	if !strings.Contains(reply, "Agents") {
		t.Errorf("reply should name the missing grant: %q", reply)
	}
}

// A known, approved sender passes and the turn runs as THEM, not the owner.
func TestResolveSessionOwnerUsesTheSender(t *testing.T) {
	f := &fakeUsers{byExternal: map[string]string{"8812": "u-yoga"}}
	c := chanWith(f)

	if reply := c.checkSenderIdentity(msgFrom(8812, "Yoga")); reply != "" {
		t.Fatalf("an approved sender was refused: %q", reply)
	}
	got, ok := c.resolveSessionOwner(msgFrom(8812, "Yoga"))
	if !ok || got != "u-yoga" {
		t.Fatalf("resolved %q ok=%v, want u-yoga", got, ok)
	}
	// The link is recorded only once the sender is cleared, so an admin
	// cannot mistake a refused sender's row for working access.
	if f.recorded["8812"] != "u-yoga" {
		t.Errorf("identity not recorded: %v", f.recorded)
	}
}

// An install that never wired identity keeps working exactly as before:
// no gate, and the turn runs as the channel owner.
func TestNoResolverKeepsLegacyBehaviour(t *testing.T) {
	c := chanWith(nil)
	if reply := c.checkSenderIdentity(msgFrom(8812, "Yoga")); reply != "" {
		t.Errorf("unwired identity should not gate anything, got %q", reply)
	}
	if _, ok := c.resolveSessionOwner(msgFrom(8812, "Yoga")); ok {
		t.Error("unwired identity should not resolve an owner")
	}
}

// A bot has an id but no person behind it — there is no identity to act as.
func TestBotSenderIsRefused(t *testing.T) {
	c := chanWith(&fakeUsers{autoRegister: true})
	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 42, FirstName: "Helper", IsBot: true}}
	if reply := c.checkSenderIdentity(msg); reply == "" {
		t.Error("a bot sender was allowed through")
	}
}
