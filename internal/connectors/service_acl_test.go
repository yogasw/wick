package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// adminModule returns a connector with one regular op and one admin-only op.
func adminModule() connector.Module {
	type In struct {
		V string `wick:"required"`
	}
	noop := func(c *connector.Ctx) (any, error) { return "ok", nil }
	return connector.Module{
		Meta:    connector.Meta{Key: "acl-stub", Name: "ACL Stub", Description: "access control test"},
		Configs: nil,
		Operations: []connector.Operation{
			connector.Op("read", "Read", "readable by all", In{}, noop, wickdocs.Docs{}),
			connector.OpDestructive("write", "Write", "admin-restricted", In{}, noop, wickdocs.Docs{}),
		},
	}
}

func newSvcACL(t *testing.T) (*Service, string) {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{adminModule()}))
	rows, _ := svc.List(context.Background())
	require.NotEmpty(t, rows)
	// enable the destructive op so it's not blocked by the Enabled toggle
	require.NoError(t, svc.SetOperationEnabled(context.Background(), rows[0].ID, "write", true))
	return svc, rows[0].ID
}

func params(connID, opKey string, isAdmin bool) ExecuteParams {
	return ExecuteParams{
		ConnectorID:  connID,
		OperationKey: opKey,
		Input:        map[string]string{"v": "x"},
		Source:       entity.ConnectorRunSourceTest,
		UserID:       "user-1",
		IsAdmin:      isAdmin,
	}
}

func TestNonAdminCanCallOpenOp(t *testing.T) {
	svc, id := newSvcACL(t)
	res, err := svc.Execute(context.Background(), params(id, "read", false))
	require.NoError(t, err)
	assert.Equal(t, entity.ConnectorRunStatusSuccess, res.Status)
}

func TestAdminCanCallOpenOp(t *testing.T) {
	svc, id := newSvcACL(t)
	res, err := svc.Execute(context.Background(), params(id, "read", true))
	require.NoError(t, err)
	assert.Equal(t, entity.ConnectorRunStatusSuccess, res.Status)
}

func TestNonAdminBlockedOnAdminOnlyOp(t *testing.T) {
	svc, id := newSvcACL(t)
	require.NoError(t, svc.SetOperationAdminOnly(context.Background(), id, "write", true))

	_, err := svc.Execute(context.Background(), params(id, "write", false))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restricted to admin")
}

func TestAdminCanCallAdminOnlyOp(t *testing.T) {
	svc, id := newSvcACL(t)
	require.NoError(t, svc.SetOperationAdminOnly(context.Background(), id, "write", true))

	res, err := svc.Execute(context.Background(), params(id, "write", true))
	require.NoError(t, err)
	assert.Equal(t, entity.ConnectorRunStatusSuccess, res.Status)
}

func TestAdminOnlyDefaultIsFalse(t *testing.T) {
	svc, id := newSvcACL(t)
	// No SetOperationAdminOnly called — default should be open
	res, err := svc.Execute(context.Background(), params(id, "write", false))
	require.NoError(t, err)
	assert.Equal(t, entity.ConnectorRunStatusSuccess, res.Status)
}

func TestSetOperationAdminOnlyCanBeReverted(t *testing.T) {
	svc, id := newSvcACL(t)
	require.NoError(t, svc.SetOperationAdminOnly(context.Background(), id, "write", true))
	// non-admin blocked
	_, err := svc.Execute(context.Background(), params(id, "write", false))
	require.Error(t, err)

	// revert
	require.NoError(t, svc.SetOperationAdminOnly(context.Background(), id, "write", false))
	// non-admin now allowed
	res, err := svc.Execute(context.Background(), params(id, "write", false))
	require.NoError(t, err)
	assert.Equal(t, entity.ConnectorRunStatusSuccess, res.Status)
}
