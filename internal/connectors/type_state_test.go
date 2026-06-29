package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// newSvcType builds a service with one Fixed connector and returns the service
// + the seeded instance ID, so a test can exercise the type-level off-switch.
func newSvcType(t *testing.T) (*Service, string) {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)

	noop := func(c *connector.Ctx) (any, error) { return "ok", nil }
	mod := connector.Module{
		Meta: connector.Meta{Key: "type-stub", Name: "Type Stub", Description: "type switch test", Fixed: true},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Op("read", "Read", "readable", struct{}{}, noop, wickdocs.Docs{}),
			),
		},
	}
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{mod}))
	rows, _ := svc.List(context.Background())
	require.NotEmpty(t, rows)
	require.NoError(t, svc.SetOperationEnabled(context.Background(), rows[0].ID, "read", true))
	return svc, rows[0].ID
}

func TestTypeEnabledDefaultsOn(t *testing.T) {
	svc, _ := newSvcType(t)
	if !svc.TypeEnabled("type-stub") {
		t.Fatal("a connector with no overlay row should default to enabled")
	}
	if !svc.TypeEnabled("never-seen") {
		t.Fatal("an unknown key should default to enabled")
	}
}

func TestSetTypeEnabledRoundTrip(t *testing.T) {
	svc, _ := newSvcType(t)

	require.NoError(t, svc.SetTypeEnabled("type-stub", false))
	if svc.TypeEnabled("type-stub") {
		t.Fatal("after SetTypeEnabled(false) the type must read disabled")
	}
	if !svc.DisabledTypeKeys()["type-stub"] {
		t.Fatal("DisabledTypeKeys must contain a disabled type")
	}

	require.NoError(t, svc.SetTypeEnabled("type-stub", true))
	if !svc.TypeEnabled("type-stub") {
		t.Fatal("re-enabling must flip it back on")
	}
	if svc.DisabledTypeKeys()["type-stub"] {
		t.Fatal("DisabledTypeKeys must not contain a re-enabled type")
	}
}

// A disabled connector TYPE blocks Execute even when the instance row + op are
// both enabled — the off-switch is the manager header kebab's behaviour.
func TestExecuteBlockedWhenTypeDisabled(t *testing.T) {
	svc, id := newSvcType(t)
	ctx := context.Background()

	p := ExecuteParams{ConnectorID: id, OperationKey: "read", Input: map[string]string{}, UserID: "u", IsAdmin: true}

	if _, err := svc.Execute(ctx, p); err != nil {
		t.Fatalf("precondition: enabled type should execute: %v", err)
	}

	require.NoError(t, svc.SetTypeEnabled("type-stub", false))
	if _, err := svc.Execute(ctx, p); err == nil {
		t.Fatal("Execute must fail while the connector type is disabled")
	}

	require.NoError(t, svc.SetTypeEnabled("type-stub", true))
	if _, err := svc.Execute(ctx, p); err != nil {
		t.Fatalf("Execute should work again after re-enable: %v", err)
	}
}

// A nil typeState store (Service built without a DB) treats every type as
// enabled and never panics.
func TestTypeStateNilSafe(t *testing.T) {
	s := &Service{}
	if !s.TypeEnabled("anything") {
		t.Fatal("nil typeState must default to enabled")
	}
	if err := s.SetTypeEnabled("anything", false); err != nil {
		t.Fatalf("nil typeState SetTypeEnabled should no-op, got %v", err)
	}
	if len(s.DisabledTypeKeys()) != 0 {
		t.Fatal("nil typeState DisabledTypeKeys should be empty")
	}
}
