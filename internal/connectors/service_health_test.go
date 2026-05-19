package connectors

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// healthModule returns a module with three ops and a HealthCheck hook
// whose report is driven by the test via the closure variable. Lets a
// test simulate "first run all fail, second run all pass" to exercise
// both the SetSystemDisabled and ClearSystemDisabled paths.
func healthModule(report *[]connector.OpHealth, callErr *error) connector.Module {
	noop := func(c *connector.Ctx) (any, error) { return "ok", nil }
	return connector.Module{
		Meta: connector.Meta{Key: "health-stub", Name: "Health Stub"},
		Operations: []connector.Operation{
			connector.Op("a", "A", "first", struct{}{}, noop, wickdocs.Docs{}),
			connector.Op("b", "B", "second", struct{}{}, noop, wickdocs.Docs{}),
			connector.Op("c", "C", "third", struct{}{}, noop, wickdocs.Docs{}),
		},
		HealthCheck: func(c *connector.Ctx) ([]connector.OpHealth, error) {
			if callErr != nil && *callErr != nil {
				return nil, *callErr
			}
			return *report, nil
		},
	}
}

func newSvcHealth(t *testing.T, mod connector.Module) (*Service, string) {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{mod}))
	rows, _ := svc.List(context.Background())
	require.NotEmpty(t, rows)
	return svc, rows[0].ID
}

func TestRunHealthCheck_LocksFailingOps(t *testing.T) {
	report := []connector.OpHealth{
		{Key: "a", OK: true},
		{Key: "b", OK: false, Reason: "needs scope: chat:write"},
		{Key: "c", OK: false, Reason: "needs scope: users:read"},
	}
	svc, id := newSvcHealth(t, healthModule(&report, nil))

	result, err := svc.RunHealthCheck(context.Background(), id)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"b", "c"}, result.NewlyLocked)
	assert.Empty(t, result.NewlyCleared)

	states, err := svc.OperationStatesFull(context.Background(), id, "health-stub")
	require.NoError(t, err)
	assert.False(t, states["a"].SystemDisabled)
	assert.True(t, states["b"].SystemDisabled)
	assert.Equal(t, "needs scope: chat:write", states["b"].SystemDisabledReason)
	assert.True(t, states["c"].SystemDisabled)
}

func TestRunHealthCheck_ClearsRecoveredOps(t *testing.T) {
	report := []connector.OpHealth{
		{Key: "a", OK: false, Reason: "needs scope: chat:write"},
		{Key: "b", OK: true},
	}
	svc, id := newSvcHealth(t, healthModule(&report, nil))

	// Round 1: a locked, b ok.
	_, err := svc.RunHealthCheck(context.Background(), id)
	require.NoError(t, err)
	st, _ := svc.OperationStatesFull(context.Background(), id, "health-stub")
	require.True(t, st["a"].SystemDisabled)

	// Round 2: both ok — a should be cleared.
	report = []connector.OpHealth{
		{Key: "a", OK: true},
		{Key: "b", OK: true},
	}
	result, err := svc.RunHealthCheck(context.Background(), id)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a"}, result.NewlyCleared)
	assert.Empty(t, result.NewlyLocked)

	st, _ = svc.OperationStatesFull(context.Background(), id, "health-stub")
	assert.False(t, st["a"].SystemDisabled)
	assert.Empty(t, st["a"].SystemDisabledReason)
}

func TestRunHealthCheck_NoHook(t *testing.T) {
	mod := connector.Module{
		Meta:       connector.Meta{Key: "no-hc", Name: "No Hook"},
		Operations: []connector.Operation{},
	}
	svc, id := newSvcHealth(t, mod)
	_, err := svc.RunHealthCheck(context.Background(), id)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoHealthCheck))
}

func TestRunHealthCheck_PropagatesHookError(t *testing.T) {
	report := []connector.OpHealth{}
	upstreamErr := errors.New("invalid_auth")
	svc, id := newSvcHealth(t, healthModule(&report, &upstreamErr))

	_, err := svc.RunHealthCheck(context.Background(), id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_auth")
}

func TestExecute_BlockedBySystemDisabled(t *testing.T) {
	report := []connector.OpHealth{{Key: "a", OK: false, Reason: "needs scope: chat:write"}}
	svc, id := newSvcHealth(t, healthModule(&report, nil))
	_, err := svc.RunHealthCheck(context.Background(), id)
	require.NoError(t, err)

	// "a" is system-disabled now — Execute must refuse before dispatch.
	_, err = svc.Execute(context.Background(), params(id, "a", true))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "system-disabled")
	assert.Contains(t, err.Error(), "chat:write")
}

func TestOperationStates_FoldsSystemDisabled(t *testing.T) {
	report := []connector.OpHealth{{Key: "a", OK: false, Reason: "x"}}
	svc, id := newSvcHealth(t, healthModule(&report, nil))
	_, err := svc.RunHealthCheck(context.Background(), id)
	require.NoError(t, err)

	// Legacy bool-map view must report effective=false even though the
	// admin Enabled flag is still true (default for non-destructive).
	states, err := svc.OperationStates(context.Background(), id, "health-stub")
	require.NoError(t, err)
	assert.False(t, states["a"], "system-disabled op should be effective=false")
}
