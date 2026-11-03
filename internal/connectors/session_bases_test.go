package connectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// defModule is a multi-row module with no saved instance, offered as a
// session base straight from its definition.
func defModule(fromDef bool) connector.Module {
	m := adminModule()
	m.Meta.Key, m.Meta.Name, m.Meta.Fixed = "def-stub", "Def Stub", false
	m.AllowSessionConfig = fromDef
	m.SessionBaseFromDefinition = fromDef
	return m
}

func baseKeys(mods []connector.Module) []string {
	out := make([]string, 0, len(mods))
	for _, m := range mods {
		out = append(out, m.Meta.Key)
	}
	return out
}

func TestSessionBasesFromDefinition(t *testing.T) {
	svc, id := newSvcACL(t)
	bg := context.Background()
	require.NoError(t, svc.UpsertModule(bg, defModule(true)))
	rows, err := svc.ListByKey(bg, "def-stub")
	require.NoError(t, err)
	require.Empty(t, rows, "the definition base must not need a saved instance")

	t.Run("listed with no instance, for anyone", func(t *testing.T) {
		assert.Equal(t, []string{"def-stub"}, baseKeys(svc.SessionBases(bg, "user-1", nil, false)))
		other := WithAgentScope(bg, stubScope{conn: "some-other-connector"})
		assert.Equal(t, []string{"def-stub"}, baseKeys(svc.SessionBases(other, "user-1", nil, false)))
	})

	t.Run("instance-backed bases still need the per-instance toggle", func(t *testing.T) {
		mod := adminModule()
		mod.AllowSessionConfig = true
		require.NoError(t, svc.UpsertModule(bg, mod))
		assert.NotContains(t, baseKeys(svc.SessionBases(bg, "user-1", nil, true)), "acl-stub")
		require.NoError(t, svc.SetSessionConfigAllowed(bg, id, true))
		assert.Equal(t, []string{"acl-stub", "def-stub"}, baseKeys(svc.SessionBases(bg, "user-1", nil, true)))
	})

	t.Run("an agent runs a definition base without a grant", func(t *testing.T) {
		scoped := WithAgentScope(bg, stubScope{conn: "some-other-connector"})
		_, err := svc.Execute(scoped, ExecuteParams{
			ConnectorID:     "sw_def-instance",
			OperationKey:    "read",
			Input:           map[string]string{"v": "x"},
			Source:          entity.ConnectorRunSourceTest,
			UserID:          "user-1",
			SessionInstance: &SessionInstanceTarget{BaseKey: "def-stub", Label: "Def Stub (session)"},
		})
		require.NoError(t, err)
	})

	t.Run("definition toggle off hides the base again", func(t *testing.T) {
		require.NoError(t, svc.UpsertModule(bg, defModule(false)))
		assert.NotContains(t, baseKeys(svc.SessionBases(bg, "user-1", nil, false)), "def-stub")
	})
}
