package view

// CustomDefInfo decorates the connector list/detail pages when the key
// belongs to a custom definition: the "Custom · <source>" badge, the
// edit-definition link, the reload banner (Dirty), the disable toggle,
// and the delete-definition danger row. MCP defs additionally carry the
// connection status chip (Connected / Disconnected, from the server
// row's last probe) and the Re-sync action — cURL/manual defs have no
// connection to track. Nil on built-in connectors.
type CustomDefInfo struct {
	DefID       string
	SourceLabel string // "cURL" | "MCP" | "Manual"
	Dirty       bool
	Disabled    bool
	MCP         bool
	Connected   bool // last tools/list probe OK (MCP only)
	Tested      bool // server has been probed at least once (MCP only)
	OAuth       bool // server uses the oauth scheme (per-instance accounts)
	// OAuthAccount is the viewed instance's resolved identity (email /
	// username from OIDC) — header chip on the detail page; empty when
	// the server yields no identity (chip hidden). OAuthConnected says
	// whether an access token is attached at all — drives the
	// Connect vs Reconnect button independent of identity.
	OAuthAccount   string
	OAuthConnected bool
}
