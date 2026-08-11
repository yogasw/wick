package configs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The validator exists because a config row has several write doors and a
// rule enforced at only some of them is not a rule. These pin that the
// hook sits on the shared write path, rejects before persisting, and does
// not disturb owners that never registered one.

func TestRegisterValidatorRejectsBeforePersisting(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()

	if err := svc.EnsureOwned(ctx, "agents", entity.Config{
		Key: "widget_frame_src", Type: "dropdown", Value: "block",
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	svc.RegisterValidator("agents", func(key, value string) error {
		if key == "widget_frame_src" && value == "nonsense" {
			return errors.New("unknown mode")
		}
		return nil
	})

	if err := svc.SetOwned(ctx, "agents", "widget_frame_src", "nonsense"); err == nil {
		t.Fatal("invalid value accepted")
	}
	// The rejected write must leave the stored value untouched, not clear it.
	if got := svc.GetOwned("agents", "widget_frame_src"); got != "block" {
		t.Fatalf("stored value changed on a rejected write: %q", got)
	}

	if err := svc.SetOwned(ctx, "agents", "widget_frame_src", "all"); err != nil {
		t.Fatalf("valid value rejected: %v", err)
	}
	if got := svc.GetOwned("agents", "widget_frame_src"); got != "all" {
		t.Fatalf("valid write not stored: %q", got)
	}
}

func TestValidatorErrorReachesCaller(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.EnsureOwned(ctx, "agents", entity.Config{Key: "k", Type: "text"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	svc.RegisterValidator("agents", func(_, _ string) error {
		return errors.New("host must not contain a path")
	})

	err := svc.SetOwned(ctx, "agents", "k", "example.com/path")
	if err == nil || !strings.Contains(err.Error(), "must not contain a path") {
		t.Fatalf("validator message lost, got: %v", err)
	}
}

func TestValidatorScopedToItsOwner(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	for _, owner := range []string{"agents", "connector:abc"} {
		if err := svc.EnsureOwned(ctx, owner, entity.Config{Key: "k", Type: "text"}); err != nil {
			t.Fatalf("ensure %s: %v", owner, err)
		}
	}
	svc.RegisterValidator("agents", func(_, _ string) error { return errors.New("nope") })

	if err := svc.SetOwned(ctx, "agents", "k", "x"); err == nil {
		t.Fatal("agents validator did not run")
	}
	// A different owner must be unaffected — this hook is registered
	// per-owner precisely so one module cannot police another's rows.
	if err := svc.SetOwned(ctx, "connector:abc", "k", "x"); err != nil {
		t.Fatalf("unrelated owner blocked by another owner's validator: %v", err)
	}
}

func TestNoValidatorAcceptsAnything(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.EnsureOwned(ctx, "agents", entity.Config{Key: "k", Type: "text"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := svc.SetOwned(ctx, "agents", "k", "anything ; goes"); err != nil {
		t.Fatalf("write blocked with no validator registered: %v", err)
	}
}

// An empty submit on a secret row means "keep what is stored" and returns
// before the write. The validator must not fire there — it would reject a
// no-op the operator never made.
func TestValidatorSkippedOnSecretKeepExisting(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.EnsureOwned(ctx, "agents", entity.Config{
		Key: "token", Type: "text", IsSecret: true,
	}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := svc.SetOwned(ctx, "agents", "token", "real-value"); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	called := false
	svc.RegisterValidator("agents", func(_, _ string) error {
		called = true
		return errors.New("should not run")
	})

	if err := svc.SetOwned(ctx, "agents", "token", ""); err != nil {
		t.Fatalf("empty secret submit errored: %v", err)
	}
	if called {
		t.Error("validator ran on a keep-existing no-op")
	}
	if got := svc.GetOwned("agents", "token"); got != "real-value" {
		t.Fatalf("secret clobbered: %q", got)
	}
}
