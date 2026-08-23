package manager

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	tool "github.com/yogasw/wick/pkg/tool"
)

func mod(key string, tagList ...tool.DefaultTag) connector.Module {
	return connector.Module{Meta: connector.Meta{Key: key, DefaultTags: tagList}}
}

var (
	visPlainUser = &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	visAdminUser = &entity.User{ID: "u2", Role: entity.RoleAdmin, Approved: true}
)

// TestIsConnectorVisibleTo_CatalogueForEveryone is the core rule: the
// connector list is a CATALOGUE of what wick can connect to, not an inventory
// of what an admin already configured. A connector with zero instances used to
// be hidden from non-admins, which made the whole list vanish for them and
// left no way to discover a connector or ask for one.
func TestIsConnectorVisibleTo_CatalogueForEveryone(t *testing.T) {
	m := mod("notion", tags.Connector)
	if !isConnectorVisibleTo(m, visPlainUser, nil) {
		t.Fatal("plain user cannot see a normal connector — the catalogue is empty for them")
	}
	if !isConnectorVisibleTo(m, visAdminUser, nil) {
		t.Fatal("admin cannot see a normal connector")
	}
	// Instance count is irrelevant here by design: this decides TYPE
	// visibility, and instance scoping happens on the rows endpoint.
}

// TestIsConnectorVisibleTo_SystemHiddenFromUsers keeps wick's own maintenance
// surface (wickmanager, sub-agents, custom-connector plumbing) out of the
// catalogue — those are not things a user connects to.
func TestIsConnectorVisibleTo_SystemHiddenFromUsers(t *testing.T) {
	m := mod("wickmanager", tags.Connector, tags.System)
	if isConnectorVisibleTo(m, visPlainUser, nil) {
		t.Fatal("system connector exposed to a plain user")
	}
	if !isConnectorVisibleTo(m, visAdminUser, nil) {
		t.Fatal("admin lost access to a system connector")
	}
}

// TestIsConnectorVisibleTo_TypeDisabledHiddenFromUsers: an admin switching a
// TYPE off declares it unavailable on this install, so it leaves the user's
// catalogue. Admins keep seeing it (greyed) so they can switch it back on.
func TestIsConnectorVisibleTo_TypeDisabledHiddenFromUsers(t *testing.T) {
	m := mod("slack", tags.Connector)
	off := map[string]bool{"slack": true}

	if isConnectorVisibleTo(m, visPlainUser, off) {
		t.Fatal("type-disabled connector still listed for a plain user")
	}
	if !isConnectorVisibleTo(m, visAdminUser, off) {
		t.Fatal("admin cannot see a type-disabled connector to re-enable it")
	}
	// Re-enabled: back in the catalogue.
	if !isConnectorVisibleTo(m, visPlainUser, map[string]bool{}) {
		t.Fatal("re-enabled connector did not come back for the user")
	}
}

// TestIsConnectorVisibleTo_NilUser covers unauthenticated / stdio callers:
// they are not admins, so they get the user rules, never the admin bypass.
func TestIsConnectorVisibleTo_NilUser(t *testing.T) {
	if !isConnectorVisibleTo(mod("notion", tags.Connector), nil, nil) {
		t.Error("nil user cannot see a normal connector")
	}
	if isConnectorVisibleTo(mod("wickmanager", tags.Connector, tags.System), nil, nil) {
		t.Error("nil user treated as admin for a system connector")
	}
	if isConnectorVisibleTo(mod("slack", tags.Connector), nil, map[string]bool{"slack": true}) {
		t.Error("nil user saw a type-disabled connector")
	}
}
