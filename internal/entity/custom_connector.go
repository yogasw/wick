package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CustomConnectorSource describes where a custom connector definition
// came from. Display-only — the generic executor behaves identically
// for all three; each op's mcp_source decides the proxy path.
type CustomConnectorSource string

const (
	CustomConnectorSourceCurl   CustomConnectorSource = "curl"
	CustomConnectorSourceMCP    CustomConnectorSource = "mcp"
	CustomConnectorSourceManual CustomConnectorSource = "manual"
)

// CustomConnector is one admin-built custom connector definition — the
// whole definition in a single row. Built-in connectors live in Go code
// under internal/connectors/* and register through RegisterBuiltins;
// custom ones live here and are replayed into the same registry at boot
// (and on admin save) by internal/connectors/custom. From the MCP
// surface the two are indistinguishable — same tool_id shape, same
// audit trail, same encrypted-fields layer.
//
// Key shares the namespace with built-in connector Meta.Keys; the save
// path validates uniqueness across both so a custom def can never
// shadow a built-in module. Instance rows, per-instance config values,
// per-op enable state, and run history all ride the existing tables
// (connectors, configs, connector_operations, connector_runs) exactly
// like a built-in.
//
// Configs holds the per-instance field schema as a JSON array of
// custom.DefField ({key, label, widget, secret, required, default,
// desc}) — mirroring what entity.StructToConfigs produces so a
// connector.Module can be assembled without Go reflection. Ops holds
// the operations as a JSON array of custom.DefOp; array order is
// display order. SourceMeta keeps provenance (chosen category tag, MCP
// server id). Raw AI-parser pastes are never persisted.
type CustomConnector struct {
	ID          string                `gorm:"type:varchar(36);primaryKey"`
	Key         string                `gorm:"type:varchar(100);uniqueIndex;not null"`
	Name        string                `gorm:"type:varchar(255);not null"`
	Description string                `gorm:"type:text"`
	// Icon is an emoji, an inline <svg>, or a data:image/...;base64
	// payload (validated to 32KB) — rendered as text or <img> by the UI.
	Icon string `gorm:"type:text"`
	Source      CustomConnectorSource `gorm:"type:varchar(16);not null"`
	SourceMeta  string                `gorm:"type:text"`
	Configs     string                `gorm:"type:text;not null;default:'[]'"`
	Ops         string                `gorm:"type:text;not null;default:'[]'"`
	CreatedBy   string                `gorm:"type:varchar(36)"`
	// SingleInstance locks the def to one row (Meta.Fixed). Default off:
	// custom connectors behave like built-ins — admins add/duplicate
	// instance rows, each with its own credentials.
	SingleInstance bool `gorm:"default:false"`
	// AllowSessionConfig is the capability flag mirrored onto
	// Module.AllowSessionConfig: when true this def's configs (base_url,
	// keys, …) may be overridden per agent session. Default off; an admin
	// still has to enable the per-instance toggle before any override is
	// accepted. Only meaningful for curl/manual API defs — leave off for
	// oauth/sso-backed MCP defs whose config is a user token.
	AllowSessionConfig bool `gorm:"default:false"`
	Disabled           bool `gorm:"default:false"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (CustomConnector) TableName() string { return "custom_connectors" }

func (d *CustomConnector) BeforeCreate(tx *gorm.DB) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	return nil
}

// CustomConnectorMCPServer is one MCP server registered as a custom
// connector source (and runtime proxy target). One server row = one
// connector definition: every tool the server lists is exposed as an
// operation automatically, minus the names in ExcludedTools — nothing
// per-tool is persisted, so tools added on the server side appear
// after a module rebuild (boot / reload / server save) without any
// wick-side change. Wick is a forwarder only: it stores the
// streamable-HTTP URL plus auth material and fires per-call JSON-RPC
// (initialize / tools/list / tools/call) over the shared HTTP client.
// No process spawn, no lifecycle — stdio transports are out of scope
// (the Transport column is reserved so a future value doesn't need a
// migration). Tool catalogs are never cached: every module build
// re-hits tools/list live.
//
// AuthScheme picks how the outbound call authenticates:
//   - "none"          — Content-Type/Accept only.
//   - "bearer"        — AuthSecret (stored encrypted under the master
//     key, decrypted per request) as Authorization: Bearer.
//   - "custom_header" — AuthHeaders JSON ([{key, value, secret}]);
//     secret values stored encrypted.
//   - "sso"           — no stored secret; wick mints a short-lived
//     ED25519 JWT for the calling user per request (AuthExtra JSON:
//     {audience, ttl_seconds}) and sends it as X-Wick-User.
//
// Headers carries extra non-auth rows (routing, tenancy) appended on
// top of the scheme's headers for every call, same JSON shape as
// AuthHeaders.
//
// LastTestAt/LastTestOK gate the save flow: a row may only be created
// after at least one successful initialize + tools/list round-trip, so
// half-broken servers never pollute the table.
type CustomConnectorMCPServer struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	Label       string `gorm:"type:varchar(255);not null"`
	Transport   string `gorm:"type:varchar(16);not null;default:'http'"`
	URL         string `gorm:"type:text;not null"`
	AuthScheme  string `gorm:"type:varchar(20);not null;default:'none'"`
	AuthSecret  string `gorm:"type:text"`
	AuthHeaders string `gorm:"type:text"`
	AuthExtra   string `gorm:"type:text"`
	Headers     string `gorm:"type:text"`
	// ExcludedTools is a JSON array of tool names the connector must NOT
	// expose. The exclusion model is opt-out: everything the server
	// lists is an operation unless its name is in here.
	ExcludedTools string `gorm:"type:text;not null;default:'[]'"`
	// ServerInfo is the JSON {name, version} the server reported on the
	// last successful initialize — admin-facing only (edit form,
	// def_get); deliberately never exposed to the LLM via wick_list.
	ServerInfo string `gorm:"type:text"`
	LastTestAt    *time.Time
	LastTestOK    bool `gorm:"default:false"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (CustomConnectorMCPServer) TableName() string { return "custom_connector_mcp_servers" }

func (m *CustomConnectorMCPServer) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}
