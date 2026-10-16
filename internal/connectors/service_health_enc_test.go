package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// tokenHealthModule reports op "probe" OK only when the hook receives
// the plaintext token, and records the value it saw.
func tokenHealthModule(want string, seen *string) connector.Module {
	noop := func(c *connector.Ctx) (any, error) { return "ok", nil }
	return connector.Module{
		Meta: connector.Meta{Key: "health-enc", Name: "Health Enc", Fixed: true},
		Configs: []entity.Config{
			{Key: "token", Type: "text", IsSecret: true, Required: true},
		},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Op("probe", "Probe", "probe", struct{}{}, noop, wickdocs.Docs{}),
			),
		},
		HealthCheck: func(c *connector.Ctx) ([]connector.OpHealth, error) {
			*seen = c.Cfg("token")
			return []connector.OpHealth{{Key: "probe", OK: *seen == want, Reason: "bad token"}}, nil
		},
	}
}

func TestRunHealthCheck_DecryptsUnderCallerKey(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	require.NoError(t, err)

	const plain = "u1-upstream-token-123"
	var seen string
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{tokenHealthModule(plain, &seen)}))
	rows, _ := svc.List(context.Background())
	require.NotEmpty(t, rows)
	id := rows[0].ID

	tok, err := encSvc.EncryptValue(plain, "u1")
	require.NoError(t, err)
	require.NoError(t, svc.Update(context.Background(), id, "Health Enc", map[string]string{"token": tok}, false))

	// Triggered by the token's owner: the hook gets plaintext.
	res, err := svc.RunHealthCheck(context.Background(), id, "u1")
	require.NoError(t, err)
	require.Equal(t, plain, seen)
	require.True(t, res.Ops[0].OK)

	// Another user, or no user (background run): the token stays opaque
	// and the hook reports the failure — no panic, no error.
	for _, caller := range []string{"u2", ""} {
		seen = ""
		res, err = svc.RunHealthCheck(context.Background(), id, caller)
		require.NoError(t, err, "caller %q", caller)
		require.NotEqual(t, plain, seen, "caller %q", caller)
		require.True(t, enc.IsToken(seen), "caller %q should see ciphertext, got %q", caller, seen)
		require.False(t, res.Ops[0].OK, "caller %q", caller)
	}
}

func TestHealthCheckSessionInstance_DecryptsUnderCallerKey(t *testing.T) {
	t.Setenv("WICK_ENC_DISABLE", "")
	encSvc, err := enc.New(testEncKey)
	require.NoError(t, err)

	const plain = "u1-session-token-456"
	var seen string
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetEnc(encSvc)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{tokenHealthModule(plain, &seen)}))

	tok, err := encSvc.EncryptValue(plain, "u1")
	require.NoError(t, err)
	cfg := map[string]string{"token": tok}

	report, err := svc.HealthCheckSessionInstance(context.Background(), "health-enc", "sw_1", cfg, "u1")
	require.NoError(t, err)
	require.Equal(t, plain, seen)
	require.True(t, report[0].OK)

	for _, caller := range []string{"u2", ""} {
		seen = ""
		report, err = svc.HealthCheckSessionInstance(context.Background(), "health-enc", "sw_1", cfg, caller)
		require.NoError(t, err, "caller %q", caller)
		require.True(t, enc.IsToken(seen), "caller %q should see ciphertext", caller)
		require.False(t, report[0].OK, "caller %q", caller)
	}
}
