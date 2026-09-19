package connectors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
)

// Two states could not express what per-account operations are for: with
// only a disabled-list, "absent from the list" had to mean both "inherit"
// and "on regardless", which are different answers the moment the instance
// itself turns the operation off.
func TestAccountOpOverrides(t *testing.T) {
	t.Run("nil account inherits everything", func(t *testing.T) {
		assert.Nil(t, AccountOpOverrides(nil))
	})

	t.Run("reads the override map", func(t *testing.T) {
		acc := &entity.ConnectorAccount{OpOverrides: `{"send":false,"read":true}`}
		assert.Equal(t, map[string]bool{"send": false, "read": true}, AccountOpOverrides(acc))
	})

	t.Run("falls back to the legacy disabled list as forced-OFF", func(t *testing.T) {
		// Accounts configured before the three-state model must keep the
		// behaviour they were given rather than silently opening up.
		acc := &entity.ConnectorAccount{DisabledOps: `["send","del"]`}
		assert.Equal(t, map[string]bool{"send": false, "del": false}, AccountOpOverrides(acc))
	})

	t.Run("the override map wins over the legacy list", func(t *testing.T) {
		acc := &entity.ConnectorAccount{
			DisabledOps: `["send"]`,
			OpOverrides: `{"send":true}`,
		}
		assert.Equal(t, map[string]bool{"send": true}, AccountOpOverrides(acc))
	})

	t.Run("malformed JSON inherits rather than guessing", func(t *testing.T) {
		assert.Nil(t, AccountOpOverrides(&entity.ConnectorAccount{OpOverrides: `{not json`}))
	})
}

func TestResolveAccountOps(t *testing.T) {
	instance := map[string]OpState{
		"on_at_instance":  {Enabled: true},
		"off_at_instance": {Enabled: false},
		"locked":          {Enabled: true, SystemDisabled: true, SystemDisabledReason: "needs scope: chat:write"},
	}
	keys := []string{"on_at_instance", "off_at_instance", "locked"}

	byKey := func(states []AccountOpState) map[string]AccountOpState {
		out := map[string]AccountOpState{}
		for _, st := range states {
			out[st.Key] = st
		}
		return out
	}

	t.Run("an account with no overrides follows the instance", func(t *testing.T) {
		got := byKey(ResolveAccountOps(&entity.ConnectorAccount{}, instance, keys))

		assert.True(t, got["on_at_instance"].Enabled)
		assert.False(t, got["on_at_instance"].Overridden)
		assert.False(t, got["off_at_instance"].Enabled)
		assert.False(t, got["off_at_instance"].Overridden)
		// Inherited is reported even where it equals Enabled, because the UI
		// has to say what clearing the override would fall back TO.
		assert.True(t, got["on_at_instance"].Inherited)
	})

	t.Run("an override switches an operation off that the instance allows", func(t *testing.T) {
		acc := &entity.ConnectorAccount{OpOverrides: `{"on_at_instance":false}`}
		got := byKey(ResolveAccountOps(acc, instance, keys))

		assert.False(t, got["on_at_instance"].Enabled)
		assert.True(t, got["on_at_instance"].Overridden)
		assert.True(t, got["on_at_instance"].Inherited, "the instance still says on — that is the fallback")
	})

	t.Run("an override switches an operation on that the instance has off", func(t *testing.T) {
		// The case two states could not express at all.
		acc := &entity.ConnectorAccount{OpOverrides: `{"off_at_instance":true}`}
		got := byKey(ResolveAccountOps(acc, instance, keys))

		assert.True(t, got["off_at_instance"].Enabled)
		assert.True(t, got["off_at_instance"].Overridden)
		assert.False(t, got["off_at_instance"].Inherited)
	})

	t.Run("the health-check lock is a ceiling no override can lift", func(t *testing.T) {
		// Forcing it on would only buy an error from the provider: the
		// credential genuinely lacks the permission.
		acc := &entity.ConnectorAccount{OpOverrides: `{"locked":true}`}
		got := byKey(ResolveAccountOps(acc, instance, keys))

		assert.False(t, got["locked"].Enabled)
		assert.True(t, got["locked"].SystemDisabled)
		assert.Equal(t, "needs scope: chat:write", got["locked"].Reason)
		assert.False(t, got["locked"].Inherited)
	})

	t.Run("every requested key comes back, in order", func(t *testing.T) {
		got := ResolveAccountOps(&entity.ConnectorAccount{}, instance, keys)
		require.Len(t, got, len(keys))
		for i, st := range got {
			assert.Equal(t, keys[i], st.Key)
		}
	})

	t.Run("an operation the instance does not declare is off, not on", func(t *testing.T) {
		got := byKey(ResolveAccountOps(&entity.ConnectorAccount{}, instance, []string{"unknown"}))
		assert.False(t, got["unknown"].Enabled)
	})
}

func TestAccountOpEnabled(t *testing.T) {
	cases := []struct {
		name  string
		acc   *entity.ConnectorAccount
		st    OpState
		opKey string
		want  bool
	}{
		{"inherits on", &entity.ConnectorAccount{}, OpState{Enabled: true}, "send", true},
		{"inherits off", &entity.ConnectorAccount{}, OpState{Enabled: false}, "send", false},
		{"forced off beats instance on", &entity.ConnectorAccount{OpOverrides: `{"send":false}`}, OpState{Enabled: true}, "send", false},
		{"forced on beats instance off", &entity.ConnectorAccount{OpOverrides: `{"send":true}`}, OpState{Enabled: false}, "send", true},
		{"system lock beats forced on", &entity.ConnectorAccount{OpOverrides: `{"send":true}`}, OpState{Enabled: true, SystemDisabled: true}, "send", false},
		{"an override for another op does not leak", &entity.ConnectorAccount{OpOverrides: `{"other":true}`}, OpState{Enabled: false}, "send", false},
		{"legacy list still disables", &entity.ConnectorAccount{DisabledOps: `["send"]`}, OpState{Enabled: true}, "send", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, AccountOpEnabled(tc.acc, tc.opKey, tc.st))
		})
	}
}
