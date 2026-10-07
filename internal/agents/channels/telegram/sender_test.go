package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestSenderForFullName(t *testing.T) {
	msg := &tgbotapi.Message{From: &tgbotapi.User{
		ID:        8812,
		FirstName: "Yoga",
		LastName:  "Setiawan",
		UserName:  "yoga",
	}}
	got := senderFor(msg, "")
	if got == nil {
		t.Fatal("got nil, want a sender")
	}
	if got.ID != "8812" {
		t.Errorf("ID = %q, want 8812", got.ID)
	}
	if got.Name != "Yoga Setiawan" {
		t.Errorf("Name = %q, want Yoga Setiawan", got.Name)
	}
	if got.Handle != "yoga" {
		t.Errorf("Handle = %q, want yoga", got.Handle)
	}
	if got.Channel != "telegram" {
		t.Errorf("Channel = %q, want telegram", got.Channel)
	}
}

// LastName is optional on Telegram, so a first-name-only user must not come
// out with a trailing space in the display name.
func TestSenderForFirstNameOnly(t *testing.T) {
	got := senderFor(&tgbotapi.Message{From: &tgbotapi.User{ID: 1, FirstName: "Yoga"}}, "")
	if got.Name != "Yoga" {
		t.Errorf("Name = %q, want Yoga with no trailing space", got.Name)
	}
}

// Both name fields are optional; the handle is the last thing left to show.
func TestSenderForFallsBackToUserName(t *testing.T) {
	got := senderFor(&tgbotapi.Message{From: &tgbotapi.User{ID: 1, UserName: "yoga"}}, "")
	if got.Name != "yoga" {
		t.Errorf("Name = %q, want the username as a fallback", got.Name)
	}
}

// Channel posts carry no From. Returning nil leaves the turn unattributed
// rather than inventing a sender the pool would present as a person.
func TestSenderForNoFrom(t *testing.T) {
	if got := senderFor(&tgbotapi.Message{}, ""); got != nil {
		t.Errorf("message with no From: got %+v, want nil", got)
	}
	if got := senderFor(nil, ""); got != nil {
		t.Errorf("nil message: got %+v, want nil", got)
	}
}

// The wick account the turn runs as. Telegram cannot map a sender to a wick
// user, so it is the channel owner — and it has to be recorded, because the
// dashboard decides whose bubble is whose by comparing this field against the
// reader. Left empty, every Telegram message renders as somebody else's,
// including the reader's own.
func TestSenderForCarriesWickUserID(t *testing.T) {
	got := senderFor(&tgbotapi.Message{From: &tgbotapi.User{ID: 8812, FirstName: "Yoga"}}, "wick-owner-1")
	if got.WickUserID != "wick-owner-1" {
		t.Errorf("WickUserID = %q, want wick-owner-1", got.WickUserID)
	}
}

// The App Owner channel row carries an empty owner id. That is a real state,
// not an error — the turn is simply unattributable to a wick account.
func TestSenderForAppOwnerHasNoWickUserID(t *testing.T) {
	got := senderFor(&tgbotapi.Message{From: &tgbotapi.User{ID: 1, FirstName: "Yoga"}}, "")
	if got.WickUserID != "" {
		t.Errorf("WickUserID = %q, want empty", got.WickUserID)
	}
	if got.ID != "1" || got.Name != "Yoga" {
		t.Errorf("the rest of the sender must still be filled: %+v", *got)
	}
}
