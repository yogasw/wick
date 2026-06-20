// Package custom is the generic executor behind admin-built custom
// connectors. Built-in connectors are Go code registered through
// RegisterBuiltins; custom connectors are custom_connectors rows that
// this package replays into the very same registry as fully-resolved
// connector.Module values. Three sources produce those rows — a
// deterministic cURL parser, a registered MCP server (one server = one
// connector; its operations mirror the live tools/list minus an
// exclude list, never persisted), and a manual form — but execution
// always flows through one of two code paths here: a templated HTTP
// call (executor.go) or a JSON-RPC proxy to the MCP server
// (mcp_client.go).
//
// Design source of truth: internal/planning/todo/custom-connector/design.md.
package custom

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/yogasw/wick/pkg/entity"
)

// DefField is one field-schema entry in ConnectorDef.Configs or
// ConnectorDefOp.Inputs. It mirrors the subset of entity.Config that
// entity.StructToConfigs produces from `wick:"..."` tags, so a Module
// can be assembled from stored JSON without Go reflection.
type DefField struct {
	Key      string `json:"key"`
	Label    string `json:"label,omitempty"`
	Widget   string `json:"widget,omitempty"`
	Options  string `json:"options,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`
	Desc     string `json:"desc,omitempty"`
	// Hidden skips the field on the instance Settings page (the row is
	// still seeded and readable at runtime) — used for machine-managed
	// values like OAuth tokens.
	Hidden bool `json:"hidden,omitempty"`
}

// allowedWidgets is the subset of the wick widget grammar a custom def
// may use. kvlist/picker need module-side Go support, so they stay out.
var allowedWidgets = map[string]bool{
	"": true, "text": true, "textarea": true, "dropdown": true,
	"number": true, "checkbox": true, "bool": true, "secret": true,
	"email": true, "url": true, "date": true, "datetime": true,
}

// ToConfig converts one DefField into the entity.Config row shape the
// connector framework consumes (Module.Configs / Operation.Input).
func (f DefField) ToConfig() entity.Config {
	widget := f.Widget
	if widget == "" {
		widget = "text"
	}
	return entity.Config{
		Key:         f.Key,
		Value:       f.Default,
		Type:        widget,
		Options:     f.Options,
		IsSecret:    f.Secret || widget == "secret",
		Required:    f.Required,
		Description: f.Desc,
		Hidden:      f.Hidden,
	}
}

// FieldsToConfigs maps a DefField slice into framework config rows.
func FieldsToConfigs(fields []DefField) []entity.Config {
	out := make([]entity.Config, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.ToConfig())
	}
	return out
}

// ParseFields decodes a JSON field-schema column (ConnectorDef.Configs,
// ConnectorDefOp.Inputs). Empty input yields an empty slice.
func ParseFields(raw string) ([]DefField, error) {
	if strings.TrimSpace(raw) == "" {
		return []DefField{}, nil
	}
	var out []DefField
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse field schema: %w", err)
	}
	return out, nil
}

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidateFields enforces the field-schema invariants shared by Configs
// and Inputs: snake_case keys, no duplicates, known widgets.
func ValidateFields(fields []DefField) error {
	seen := map[string]bool{}
	for _, f := range fields {
		if !keyRe.MatchString(f.Key) {
			return fmt.Errorf("field key %q must be snake_case (a-z0-9_)", f.Key)
		}
		if seen[f.Key] {
			return fmt.Errorf("duplicate field key %q", f.Key)
		}
		seen[f.Key] = true
		if !allowedWidgets[f.Widget] {
			return fmt.Errorf("field %q: unsupported widget %q", f.Key, f.Widget)
		}
	}
	return nil
}

// OpRequest is the stored request recipe for an HTTP-backed operation
// (ConnectorDefOp.Request). URLTemplate, every header value, and
// BodyTemplate are Go text/template strings rendered against
// {.cfg.<key>} / {.in.<key>} — see template.go for the function
// whitelist.
type OpRequest struct {
	Method       string            `json:"method"`
	URLTemplate  string            `json:"url_template"`
	Headers      map[string]string `json:"headers,omitempty"`
	BodyTemplate string            `json:"body_template,omitempty"`
	ContentType  string            `json:"content_type,omitempty"`
}

var allowedMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

// Validate checks the stored recipe before it is persisted or executed.
func (r OpRequest) Validate() error {
	if !allowedMethods[strings.ToUpper(r.Method)] {
		return fmt.Errorf("unsupported HTTP method %q", r.Method)
	}
	if strings.TrimSpace(r.URLTemplate) == "" {
		return fmt.Errorf("url_template is required")
	}
	return nil
}

// MCPSource marks an operation as MCP-backed (ConnectorDefOp.MCPSource):
// execution proxies a JSON-RPC tools/call for ToolName to the
// CustomMCPServer row identified by ServerID.
type MCPSource struct {
	ServerID string `json:"server_id"`
	ToolName string `json:"tool_name"`
}

// HeaderRow is one configurable outbound header on a CustomMCPServer
// (AuthHeaders for the custom_header scheme, Headers for scheme-
// independent extras). Secret values are stored encrypted under the
// master key and decrypted per request.
type HeaderRow struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
}

// ParseHeaderRows decodes a JSON HeaderRow column. Empty input yields
// an empty slice.
func ParseHeaderRows(raw string) ([]HeaderRow, error) {
	if strings.TrimSpace(raw) == "" {
		return []HeaderRow{}, nil
	}
	var out []HeaderRow
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse header rows: %w", err)
	}
	return out, nil
}

// SSOExtra is the stored AuthExtra payload for the sso auth scheme.
type SSOExtra struct {
	Audience   string `json:"audience,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

// SourceMeta is the provenance blob stored on ConnectorDef.SourceMeta.
// Category names the visual group tag chosen at review time so reloads
// re-link the same tags; ServerID points at the MCP server for
// source=mcp defs. Raw pastes are never stored here.
//
// HealthOp/HealthExpect configure the optional connector health check:
// HealthOp names one operation to run as the probe, and the verdict is
// "healthy" when that op executes without error (HTTP 2xx + no transport
// error, or an MCP tools/call that returns a non-error result). When
// HealthExpect is non-empty, the rendered response must also contain it
// as a substring. Empty HealthOp = no health check (no chip, no button).
type SourceMeta struct {
	Category     string `json:"category,omitempty"`
	ServerID     string `json:"server_id,omitempty"`
	HealthOp     string `json:"health_op,omitempty"`
	HealthExpect string `json:"health_expect,omitempty"`
}

// ParseSourceMeta tolerates an empty or legacy column.
func ParseSourceMeta(raw string) SourceMeta {
	var m SourceMeta
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

// DefOp is one operation of a custom connector — the parser/review-form
// interchange shape shared by the cURL parser, the AI parser, the MCP
// import, and the manual builder. Ops are persisted grouped into
// DefCategory sections (entity.CustomConnector.Ops is a JSON array of
// DefCategory), never as a flat DefOp array. Exactly one of Request /
// MCPSource is set.
type DefOp struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Destructive bool       `json:"destructive"`
	Inputs      []DefField `json:"inputs"`
	Request     *OpRequest `json:"request,omitempty"`
	MCPSource   *MCPSource `json:"mcp_source,omitempty"`
}

// DefCategory is one titled section of a custom connector's operations —
// the grouping that owns its ops (mirrors connector.Category for built-in
// connectors). entity.CustomConnector.Ops is a JSON array of DefCategory;
// array order is display order, and each category's Ops slice is display
// order within the section. Title may be empty for an untitled/default
// section; non-empty titles must be unique across the connector. The op
// keys themselves must be globally unique across all categories because
// they map to MCP tool ids.
type DefCategory struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Ops         []DefOp `json:"ops"`
}

// ParseOps decodes the entity.CustomConnector.Ops JSON column into the
// nested category shape. Empty input yields an empty slice. A JSON error
// is returned as-is — the old flat DefOp array is unsupported (no
// migration / fallback).
func ParseOps(raw string) ([]DefCategory, error) {
	if strings.TrimSpace(raw) == "" {
		return []DefCategory{}, nil
	}
	var out []DefCategory
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse ops: %w", err)
	}
	return out, nil
}

// FlattenOps concatenates every category's ops in declaration order, for
// consumers that only need the flat op list (key lookups, MCP tool ids,
// per-op state) and do not care about the grouping.
func FlattenOps(cats []DefCategory) []DefOp {
	var out []DefOp
	for _, c := range cats {
		out = append(out, c.Ops...)
	}
	return out
}

// Draft is the full review-form payload: what a parse produces and what
// the save endpoint consumes. Meta fields are filled in by the admin on
// the review screen. Configs/Ops persist verbatim onto the
// entity.CustomConnector row; Category lands in SourceMeta. Ops is the
// operations grouped into DefCategory sections (the json field stays
// "ops" for the FE contract, but it is now an array of categories, each
// owning its ops). Single locks the def to one instance row — default
// off (multi-row, same as built-ins).
type Draft struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Source      string `json:"source"`
	Category    string `json:"category"`
	Single      bool   `json:"single"`
	// AllowSessionConfig opts this def into per-session config override
	// (Module.AllowSessionConfig). Default off; only meaningful for
	// curl/manual API defs, never oauth/sso token configs.
	AllowSessionConfig bool `json:"allow_session_config"`
	// HealthOp names the operation run as the connector health probe;
	// empty = no health check. HealthExpect is an optional substring the
	// probe response must contain on top of executing without error.
	HealthOp     string        `json:"health_op,omitempty"`
	HealthExpect string        `json:"health_expect,omitempty"`
	Configs      []DefField    `json:"configs"`
	Ops          []DefCategory `json:"ops"`
}

// AllOps flattens the draft's categorized operations into a single slice
// in declaration order, for callers that only need the flat op list.
func (d *Draft) AllOps() []DefOp { return FlattenOps(d.Ops) }

// maxIconBytes caps icon payloads — enough for a reasonable SVG or a
// small base64 raster, small enough to keep card pages light.
const maxIconBytes = 32 * 1024

// ValidateIcon accepts the three icon shapes the UI renders: a short
// emoji/text glyph, an inline <svg>, or a data:image/...;base64
// payload. Images render through <img>, which neutralizes any script
// an SVG could carry.
func ValidateIcon(icon string) error {
	ic := strings.TrimSpace(icon)
	if ic == "" {
		return nil // empty falls back to the default
	}
	if len(ic) > maxIconBytes {
		return fmt.Errorf("icon is larger than %dKB", maxIconBytes/1024)
	}
	switch {
	case strings.HasPrefix(ic, "<svg"):
		return nil
	case strings.HasPrefix(ic, "data:image/"):
		return nil
	case strings.HasPrefix(ic, "data:"), strings.HasPrefix(ic, "<"):
		return fmt.Errorf("icon must be an emoji, an inline <svg>, or a data:image/...;base64 payload")
	case len(ic) > 32:
		return fmt.Errorf("text icons are capped at 32 bytes — use an emoji, or paste an <svg> / data:image payload")
	}
	return nil
}

// reservedKeys can never be used as a custom connector key: "custom"
// would shadow the /manager/connectors/custom/* builder routes.
var reservedKeys = map[string]bool{"custom": true}

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ValidateDraft enforces every structural invariant before rows are
// written. Key-uniqueness against built-ins and other defs is checked
// separately by the Service (it needs registry + DB access).
func ValidateDraft(d *Draft) error {
	if !slugRe.MatchString(d.Key) {
		return fmt.Errorf("connector key %q must be a lowercase slug (a-z0-9_-)", d.Key)
	}
	if reservedKeys[d.Key] {
		return fmt.Errorf("connector key %q is reserved", d.Key)
	}
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("connector name is required")
	}
	if err := ValidateIcon(d.Icon); err != nil {
		return err
	}
	if err := ValidateFields(d.Configs); err != nil {
		return fmt.Errorf("configs: %w", err)
	}
	if len(FlattenOps(d.Ops)) == 0 {
		return fmt.Errorf("at least one operation is required")
	}
	// Op keys are globally unique across every category (they map to MCP
	// tool ids); category titles, when non-empty, must be unique too.
	seen := map[string]bool{}
	titles := map[string]bool{}
	for _, cat := range d.Ops {
		if t := strings.TrimSpace(cat.Title); t != "" {
			if titles[t] {
				return fmt.Errorf("duplicate category title %q", t)
			}
			titles[t] = true
		}
		for _, op := range cat.Ops {
			if !keyRe.MatchString(op.Key) {
				return fmt.Errorf("op key %q must be snake_case", op.Key)
			}
			if seen[op.Key] {
				return fmt.Errorf("duplicate op key %q", op.Key)
			}
			seen[op.Key] = true
			if strings.TrimSpace(op.Name) == "" || strings.TrimSpace(op.Description) == "" {
				return fmt.Errorf("op %q: name and description are required", op.Key)
			}
			if err := ValidateFields(op.Inputs); err != nil {
				return fmt.Errorf("op %q inputs: %w", op.Key, err)
			}
			switch {
			case op.Request != nil && op.MCPSource != nil:
				return fmt.Errorf("op %q: request and mcp_source are mutually exclusive", op.Key)
			case op.Request != nil:
				if err := op.Request.Validate(); err != nil {
					return fmt.Errorf("op %q: %w", op.Key, err)
				}
			case op.MCPSource != nil:
				if op.MCPSource.ServerID == "" || op.MCPSource.ToolName == "" {
					return fmt.Errorf("op %q: mcp_source needs server_id and tool_name", op.Key)
				}
			default:
				return fmt.Errorf("op %q: either request or mcp_source is required", op.Key)
			}
		}
	}
	// Health probe (optional) must point at a real operation. An expected
	// substring without a probe op is meaningless — reject it so a stale
	// form value never silently disables the whole check.
	if h := strings.TrimSpace(d.HealthOp); h != "" {
		if !seen[h] {
			return fmt.Errorf("health check operation %q is not one of this connector's operations", h)
		}
	} else if strings.TrimSpace(d.HealthExpect) != "" {
		return fmt.Errorf("health check expected value is set but no health check operation is chosen")
	}
	return nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
