package view

import (
	"time"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// UserRow is the view model for a single user row in the admin table.
type UserRow struct {
	User   *entity.User
	TagIDs []string
}

// ToolRow is the view model for a single tool row in the admin table.
type ToolRow struct {
	Tool        tool.Tool
	Visibility  entity.ToolVisibility
	Disabled    bool
	TagIDs      []string
	ConfigCount int
}

// JobRow is the view model for a single job row in the admin jobs table.
type JobRow struct {
	Job         entity.Job
	Disabled    bool
	TagIDs      []string
	ConfigCount int
}

// ConnectorAdminRow is the view model for a single connector instance in
// the admin connectors table. ModuleName/Icon come from the in-memory
// registry; when the row's Key has no registered module (deleted from
// code after a deploy) ModuleMissing is true so the UI can mark it.
type ConnectorAdminRow struct {
	Connector     entity.Connector
	ModuleName    string
	ModuleIcon    string
	ModuleMissing bool
	TagIDs        []string
}

// AccessTokenRow is the view model for one Personal Access Token in
// the admin access-tokens table. OwnerName / OwnerEmail are joined
// from the user table so the row can show who owns the token without
// an N+1 lookup. PATs are general-purpose bearers — MCP is just one
// caller — so the surface lives at /admin/access-tokens.
type AccessTokenRow struct {
	Token      entity.PersonalAccessToken
	OwnerName  string
	OwnerEmail string
}

// ConnectionRow is the view model for one (user, OAuth client) grant
// in the admin connections table. Owner fields come from the user
// table; Granted/LastUsed/TokenCount are aggregated across the active
// access + refresh tokens for that pair.
type ConnectionRow struct {
	UserID     string
	OwnerName  string
	OwnerEmail string
	ClientID   string
	ClientName string
	GrantedAt  time.Time
	LastUsedAt *time.Time
	TokenCount int
}

