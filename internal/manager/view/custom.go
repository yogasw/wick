package view

import (
	"encoding/base64"
	"strings"
)

// iconImageSrc resolves image-shaped connector icons (inline <svg> or
// data:image payloads) into an <img> src. SVGs are wrapped in a data
// URI — rendering through <img> means embedded scripts never execute.
// ok=false marks plain text/emoji icons, rendered as text.
func iconImageSrc(icon string) (string, bool) {
	ic := strings.TrimSpace(icon)
	switch {
	case strings.HasPrefix(ic, "data:image/"):
		return ic, true
	case strings.HasPrefix(ic, "<svg"):
		return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(ic)), true
	}
	return "", false
}

// ssoClaimExample is the read-only claim-mapping block on the MCP
// server form's SSO panel. Kept as a Go string because templ collapses
// whitespace in element text nodes — interpolation preserves the
// newlines a <pre> needs.
const ssoClaimExample = `{
  "sub":    "user id (uuid)",
  "email":  "user email",
  "name":   "user display name",
  "groups": "user tag ids",
  "aud":    "audience below",
  "iss":    "this wick's app URL",
  "iat":    "now", "exp": "now + TTL"
}`

// MCPToolRow is one row of the exclude-list on the MCP server form —
// the live tools/list catalog slimmed for display. Serialized into the
// page as JSON for custom_mcp_form.js to render.
type MCPToolRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

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

