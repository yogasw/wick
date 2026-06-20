package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// ssoDefaultModule is an OAuth-only-style connector that declares
// DefaultAccess so every new row starts SSO-on.
func ssoDefaultModule() connector.Module {
	noop := func(c *connector.Ctx) (any, error) { return "ok", nil }
	return connector.Module{
		Meta: connector.Meta{Key: "sso-default", Name: "SSO Default", Description: "default-access test"},
		Operations: []connector.Category{
			connector.Cat("", "", connector.Op("ping", "Ping", "noop", struct{}{}, noop, wickdocs.Docs{})),
		},
		OAuth:         &connector.OAuthMeta{DisplayName: "SSO Default"},
		DefaultAccess: connector.AccessDefaults{EnableSSO: true, AllowOthersConnectSSO: true},
	}
}

// plainModule has no DefaultAccess — rows must come out with everything off.
func plainModule() connector.Module {
	noop := func(c *connector.Ctx) (any, error) { return "ok", nil }
	return connector.Module{
		Meta: connector.Meta{Key: "plain-default", Name: "Plain", Description: "no default access"},
		Operations: []connector.Category{
			connector.Cat("", "", connector.Op("ping", "Ping", "noop", struct{}{}, noop, wickdocs.Docs{})),
		},
	}
}

func newSvcDefaults(t *testing.T) *Service {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{ssoDefaultModule(), plainModule()}))
	return svc
}

func TestCreateSeedsDefaultAccess(t *testing.T) {
	svc := newSvcDefaults(t)
	row, err := svc.Create(context.Background(), "sso-default", "Row A", nil, "user-1")
	require.NoError(t, err)
	require.True(t, row.EnableSSO, "EnableSSO should default on from DefaultAccess")
	require.True(t, row.AllowOthersConnectSSO, "AllowOthersConnectSSO should default on")
	require.False(t, row.MultiAccount, "MultiAccount stays off — not in DefaultAccess")
}

func TestCreateNoDefaultAccessStaysOff(t *testing.T) {
	svc := newSvcDefaults(t)
	row, err := svc.Create(context.Background(), "plain-default", "Row A", nil, "user-1")
	require.NoError(t, err)
	require.False(t, row.EnableSSO)
	require.False(t, row.AllowOthersConnectSSO)
}
