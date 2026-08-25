package channels

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
)

func TestSenderCtx_RoundTrip(t *testing.T) {
	base := context.Background()

	if got := SenderFrom(base); got != nil {
		t.Errorf("bare ctx sender = %+v, want nil", got)
	}

	want := &store.Sender{ID: "U0104", Name: "Yoga Setiawan", Channel: "slack"}
	ctx := WithSender(base, want)

	got := SenderFrom(ctx)
	if got == nil {
		t.Fatal("sender = nil, want the stamped value")
	}
	if *got != *want {
		t.Errorf("sender = %+v, want %+v", *got, *want)
	}
}

// A nil sender must not plant a key. The pool reads nil as "no human behind
// this turn"; a stored nil would be indistinguishable from an absent one
// while still shadowing an outer value — which would silently drop the
// identity of a turn that genuinely had one.
func TestSenderCtx_NilDoesNotShadow(t *testing.T) {
	ctx := WithSender(context.Background(), &store.Sender{ID: "U1", Channel: "slack"})

	got := SenderFrom(WithSender(ctx, nil))
	if got == nil {
		t.Fatal("a nil sender overwrote an existing one")
	}
	if got.ID != "U1" {
		t.Errorf("sender ID = %q, want U1", got.ID)
	}
}

// The sender is independent of the caller identity: they answer different
// questions (who is speaking vs whose account the turn runs as) and diverge
// whenever a channel user has no mapped wick account. Stamping one must not
// disturb the other.
func TestSenderCtx_IndependentOfCallerUserID(t *testing.T) {
	ctx := WithCallerUserID(context.Background(), "wick-user-1")
	ctx = WithSender(ctx, &store.Sender{ID: "U0104", Channel: "slack"})

	if got := CallerUserID(ctx); got != "wick-user-1" {
		t.Errorf("caller = %q, want wick-user-1", got)
	}
	if got := SenderFrom(ctx); got == nil || got.ID != "U0104" {
		t.Errorf("sender = %+v, want the Slack user ID", got)
	}
}
