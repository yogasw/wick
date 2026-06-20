package configs

import (
	"context"
	"testing"
)

func TestProfile_DefaultsToFull(t *testing.T) {
	svc := newTestSvc(t)
	if err := svc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if got := svc.Profile(); got != "full" {
		t.Fatalf("Profile() default = %q, want \"full\"", got)
	}
}

func TestProfile_ReturnsSetValue(t *testing.T) {
	svc := newTestSvc(t)
	ctx := context.Background()
	if err := svc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := svc.Set(ctx, KeyProfile, "agent"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := svc.Profile(); got != "agent" {
		t.Fatalf("Profile() = %q, want \"agent\"", got)
	}
}
