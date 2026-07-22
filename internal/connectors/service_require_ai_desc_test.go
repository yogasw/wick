package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// descModule is a module with no required config fields, so its readiness is
// driven solely by RequireAIDescription: with the flag on, a blank per-instance
// AI description must read as needs_setup.
func descModule(require bool) connector.Module {
	noop := func(c *connector.Ctx) (any, error) { return "ok", nil }
	return connector.Module{
		Meta: connector.Meta{Key: "desc-stub", Name: "Desc Stub", RequireAIDescription: require},
		Operations: []connector.Category{
			connector.Cat("", "", connector.Op("a", "A", "op", struct{}{}, noop, wickdocs.Docs{})),
		},
	}
}

func newSvcDesc(t *testing.T, mod connector.Module) *Service {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{mod}))
	return svc
}

// TestStatus_RequireAIDescription: with the flag set, a fresh instance whose AI
// description is blank is needs_setup even though it has no missing configs;
// filling the description flips it to ready; clearing it flips back.
func TestStatus_RequireAIDescription(t *testing.T) {
	ctx := context.Background()
	svc := newSvcDesc(t, descModule(true))

	c, err := svc.Create(ctx, "desc-stub", "row", nil, "tester")
	require.NoError(t, err)
	assert.Equal(t, "needs_setup", svc.Status(*c), "blank AI description should be needs_setup")

	require.NoError(t, svc.SetDescription(ctx, c.ID, "Only the Ops automation may use this."))
	c2, err := svc.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "ready", svc.Status(*c2), "filled AI description should be ready")

	require.NoError(t, svc.SetDescription(ctx, c.ID, "   "))
	c3, err := svc.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "needs_setup", svc.Status(*c3), "whitespace-only AI description should be needs_setup")
}

// TestStatus_NoRequireAIDescription: without the flag, a blank AI description is
// irrelevant — an instance with all configs satisfied is ready.
func TestStatus_NoRequireAIDescription(t *testing.T) {
	ctx := context.Background()
	svc := newSvcDesc(t, descModule(false))

	c, err := svc.Create(ctx, "desc-stub", "row", nil, "tester")
	require.NoError(t, err)
	assert.Equal(t, "ready", svc.Status(*c), "no flag: blank description must not block readiness")
}
