package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// maxHTTPResponse caps how much of an upstream response the generic
// executor reads — mirrors the audit-log truncation discipline of the
// built-in connectors.
const maxHTTPResponse = 4 << 20

// BuildModule assembles a fully-resolved connector.Module from one
// custom_connectors row. The result is indistinguishable from a
// built-in registration: same Meta/Configs/Operations shape, same Ctx
// contract, same encrypted-fields behaviour (secret DefFields become
// IsSecret config rows the framework decrypts and masks).
//
// MCP-sourced defs carry no stored ops — the operation set is the
// server's live tools/list at build time, minus the names on the
// server row's ExcludedTools. cURL/manual defs keep reading the Ops
// JSON column.
func (s *Service) BuildModule(ctx context.Context, def *entity.CustomConnector) (connector.Module, error) {
	return s.buildModuleFor(ctx, def, "")
}

// buildModuleFor is BuildModule with the caller's context (request-
// scoped logger rides it into the outbound probe logs) and a preferred
// probe instance — the per-instance re-sync runs the tools/list under
// that instance's account (MCP servers may expose different tools per
// account).
func (s *Service) buildModuleFor(ctx context.Context, def *entity.CustomConnector, preferInstance string) (connector.Module, error) {
	var ops []DefOp
	var err error
	switch {
	case def.Disabled:
		// Disabled defs keep their registration (cards, detail page,
		// instance rows stay reachable) but expose zero operations —
		// nothing is listable or callable until re-enabled.
	case def.Source == entity.CustomConnectorSourceMCP:
		ops, _ = s.liveMCPOps(ctx, def, preferInstance)
	default:
		if ops, err = ParseOps(def.Ops); err != nil {
			return connector.Module{}, fmt.Errorf("def %s: %w", def.Key, err)
		}
	}
	return s.assembleModule(ctx, def, ops)
}

// assembleModule turns a def plus its resolved operation set into the
// registry module — split from BuildModule so RefreshIfStale can keep
// the old catalog when a live probe fails instead of swapping in zero
// ops.
func (s *Service) assembleModule(ctx context.Context, def *entity.CustomConnector, ops []DefOp) (connector.Module, error) {
	cfgFields, err := ParseFields(def.Configs)
	if err != nil {
		return connector.Module{}, fmt.Errorf("def %s: %w", def.Key, err)
	}

	// oauth-scheme MCP defs carry the per-instance account fields so
	// "+ New row" seeds them and the Connect flow's SetOwned writes are
	// accepted. Hidden — managed by the flow, not typed by hand.
	if srvRow := s.serverRowForDef(ctx, def); srvRow != nil && srvRow.AuthScheme == "oauth" {
		cfgFields = append(cfgFields, oauthInstanceConfigs()...)
	}

	operations := make([]connector.Operation, 0, len(ops))
	opKeys := make([]string, 0, len(ops))
	var probe connector.ExecuteFunc
	meta := ParseSourceMeta(def.SourceMeta)
	for _, op := range ops {
		exec := s.executeFunc(cfgFields, op)
		if op.Key == meta.HealthOp {
			probe = exec
		}
		opKeys = append(opKeys, op.Key)
		operations = append(operations, connector.Operation{
			Key:         op.Key,
			Name:        op.Name,
			Description: op.Description,
			Input:       FieldsToConfigs(op.Inputs),
			Destructive: op.Destructive,
			Execute:     exec,
			Docs:        wickdocs.Docs{},
		})
	}

	// Optional health check: run the chosen probe op and project its
	// verdict onto every operation. Custom connectors share one credential
	// per instance, so a passing probe means the whole connector is
	// reachable, a failing one disables all ops — same shape as a built-in
	// like Slack projecting auth.test. Skipped when the def is disabled
	// (zero ops) or the named probe op vanished.
	var healthCheck connector.HealthCheckFunc
	if probe != nil {
		healthCheck = healthCheckFor(probe, opKeys, meta.HealthExpect)
	}

	return connector.Module{
		Meta: connector.Meta{
			Key:         def.Key,
			Name:        def.Name,
			Description: def.Description,
			Icon:        def.Icon,
			// Multi-row by default — admins add/duplicate instance rows
			// (each with its own credentials) exactly like a built-in,
			// and no row is auto-seeded until "+ New row". SingleInstance
			// opts a def into Fixed, which (as everywhere) auto-seeds its
			// one row since the UI offers no other way to create it.
			Fixed: def.SingleInstance,
			// MCP catalogs are live — zero ops can mean "not synced
			// yet", so wick_list refreshes before hiding the connector.
			LiveCatalog: def.Source == entity.CustomConnectorSourceMCP,
			DefaultTags: s.defaultTagsFor(def),
		},
		Configs:     FieldsToConfigs(cfgFields),
		Operations:  operations,
		HealthCheck: healthCheck,
		// Author opt-in: lets users override this def's config per agent
		// session (still gated by the per-instance admin toggle).
		AllowSessionConfig: def.AllowSessionConfig,
	}, nil
}

// healthCheckFor builds the Module.HealthCheck hook for a custom def: run
// the chosen probe op against the live Ctx (decrypted configs, no inputs),
// then project a single verdict onto every operation. A custom connector
// instance carries one credential, so "the probe reached upstream" means
// the whole connector is healthy; a probe failure disables all ops with
// the same reason. expect (optional) is a substring the probe response
// must contain on top of executing without error.
func healthCheckFor(probe connector.ExecuteFunc, opKeys []string, expect string) connector.HealthCheckFunc {
	expect = strings.TrimSpace(expect)
	return func(c *connector.Ctx) ([]connector.OpHealth, error) {
		ok, reason := true, ""
		out, err := probe(c)
		switch {
		case err != nil:
			ok, reason = false, err.Error()
		case expect != "":
			if !healthBodyContains(out, expect) {
				ok = false
				reason = fmt.Sprintf("response did not contain expected text %q", expect)
			}
		}
		report := make([]connector.OpHealth, 0, len(opKeys))
		for _, k := range opKeys {
			report = append(report, connector.OpHealth{Key: k, OK: ok, Reason: reason})
		}
		return report, nil
	}
}

// healthBodyContains reports whether the probe's decoded response contains
// expect. The executor returns decoded JSON (any) or a raw string; for the
// substring test we render JSON back to its compact form so a check like
// `"ok":true` matches the structured value the same way it would the wire
// bytes. Non-JSON responses are already strings and compare directly.
func healthBodyContains(out any, expect string) bool {
	switch v := out.(type) {
	case string:
		return strings.Contains(v, expect)
	case nil:
		return false
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return strings.Contains(fmt.Sprintf("%v", v), expect)
		}
		return strings.Contains(string(b), expect)
	}
}

// liveMCPProbeTimeout bounds the tools/list probe a module build fires —
// boot must not hang on an unreachable server.
const liveMCPProbeTimeout = 15 * time.Second

// serverRowForDef loads the MCP server row backing a def; nil for
// curl/manual defs or when the row is gone.
func (s *Service) serverRowForDef(parent context.Context, def *entity.CustomConnector) *entity.CustomConnectorMCPServer {
	serverID := ServerIDForDef(def)
	if serverID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	row, err := s.store.GetServer(ctx, serverID)
	if err != nil {
		return nil
	}
	return row
}

// liveMCPOps materializes an MCP def's operation set from the server's
// live tools/list. Nothing is persisted: every module build (boot,
// reload, server save) re-probes, so tools added on the server appear
// automatically and ExcludedTools names are dropped. A failed probe
// degrades to zero operations with ok=false (warn-logged) instead of
// failing the build — boot survives a down server; callers that only
// refresh (RefreshIfStale) use ok to keep the old catalog instead.
// preferInstance picks whose account authenticates an oauth probe —
// servers may expose different tools per account, so the per-instance
// re-sync passes the instance being viewed; "" falls back to the first
// enabled instance with a connected account.
func (s *Service) liveMCPOps(parent context.Context, def *entity.CustomConnector, preferInstance string) ([]DefOp, bool) {
	l := log.Ctx(parent).With().Str("component", "custom-connector").Str("key", def.Key).Logger()
	serverID := ParseSourceMeta(def.SourceMeta).ServerID
	if serverID == "" {
		l.Warn().Msg("mcp def has no server_id; exposing zero operations")
		return nil, false
	}
	// Derive from the caller so the request-scoped logger (request_id)
	// reaches the outbound MCP logs and cancellation propagates.
	ctx, cancel := context.WithTimeout(parent, liveMCPProbeTimeout)
	defer cancel()
	row, err := s.store.GetServer(ctx, serverID)
	if err != nil {
		l.Warn().Err(err).Str("server_id", serverID).Msg("mcp server row missing; exposing zero operations")
		return nil, false
	}
	srv, err := resolveServerConfig(row.URL, row.AuthScheme, row.AuthSecret, row.AuthHeaders, row.AuthExtra, row.Headers)
	if err != nil {
		l.Warn().Err(err).Msg("resolve mcp server config; exposing zero operations")
		return nil, false
	}
	var claims *SSOClaims
	if row.AuthScheme == "sso" {
		claims = systemSSOClaims()
	}
	if row.AuthScheme == "oauth" {
		// tools/list needs a token — borrow the first instance with a
		// connected account (refreshing when expired). No account yet →
		// zero ops until one connects and the def re-syncs.
		meta := parseOAuthMeta(row.AuthExtra)
		rows, _ := s.conns.ListByKey(ctx, def.Key)
		// Preferred instance first, then the rest in order.
		ordered := make([]entity.Connector, 0, len(rows))
		for _, r := range rows {
			if r.ID == preferInstance {
				ordered = append([]entity.Connector{r}, ordered...)
			} else {
				ordered = append(ordered, r)
			}
		}
		for _, r := range ordered {
			// Accounts are per instance — only enabled rows lend their
			// token to the catalog probe.
			if r.Disabled {
				continue
			}
			if tok, err := s.instanceAccessToken(ctx, &meta, r.ID); err == nil && tok != "" {
				srv.AccessToken = tok
				break
			}
		}
		if srv.AccessToken == "" {
			l.Warn().Msg("oauth mcp def has no connected account; exposing zero operations")
			return nil, false
		}
	}
	res := s.mcp(nil).Probe(ctx, srv, claims)
	// Every build doubles as a connectivity check — the LastTest columns
	// drive the Connected/Disconnected status chip in the manager.
	now := time.Now()
	row.LastTestAt = &now
	row.LastTestOK = res.OK
	if res.OK && res.ServerName != "" {
		info, _ := json.Marshal(map[string]string{"name": res.ServerName, "version": res.ServerVersion})
		row.ServerInfo = string(info)
	}
	_ = s.store.UpdateServer(ctx, row)
	if !res.OK {
		l.Warn().Str("error", res.Error).Msg("mcp tools/list probe failed; exposing zero operations")
		return nil, false
	}
	s.adoptServerDescription(ctx, def, res.Instructions)
	return toolsToOps(serverID, res.Tools, parseExcluded(row.ExcludedTools)), true
}

// adoptServerDescription replaces the auto-generated placeholder with
// the server's own initialize instructions — but never an admin-written
// description. Mutates def in place (the module being assembled picks
// it up) and persists the row.
func (s *Service) adoptServerDescription(ctx context.Context, def *entity.CustomConnector, instructions string) {
	if !isPlaceholderDescription(def.Description) {
		return
	}
	desc := snippet([]byte(instructions), 280)
	if desc == "" {
		// No instructions from the server — still migrate the legacy
		// "Live proxy …" wording to the neutral placeholder so wick_list
		// never tells the LLM this is a proxy.
		desc = mcpDescriptionPlaceholder + " '" + def.Name + "'."
	}
	if def.Description == desc {
		return
	}
	def.Description = desc
	if err := s.store.UpdateDef(ctx, def); err != nil {
		log.Warn().Err(err).Str("key", def.Key).Msg("adopt mcp server description")
	}
}

// systemSSOClaims is the synthetic identity for probes that run without
// a calling user (boot, reload). Per-call execution still forwards the
// real caller; only tool *listing* sees this subject.
func systemSSOClaims() *SSOClaims {
	return &SSOClaims{Subject: "system:wick", Name: "wick"}
}

// toolsToOps maps a live tool catalog onto DefOps, skipping excluded
// names and key collisions (two tool names slugging to the same op key
// keep the first).
func toolsToOps(serverID string, tools []MCPTool, excluded map[string]bool) []DefOp {
	out := make([]DefOp, 0, len(tools))
	seen := map[string]bool{}
	for _, t := range tools {
		if excluded[t.Name] {
			continue
		}
		key := toFieldKey(t.Name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		op := DefOp{
			Key:         key,
			Name:        humanize(key),
			Description: t.Description,
			Destructive: destructiveToolRe.MatchString(t.Name),
			Inputs:      mapInputSchema(t.InputSchema),
			MCPSource:   &MCPSource{ServerID: serverID, ToolName: t.Name},
		}
		if op.Description == "" {
			op.Description = "Proxy of MCP tool " + t.Name + "."
		}
		out = append(out, op)
	}
	return out
}

// parseExcluded decodes the ExcludedTools JSON column into a name set.
func parseExcluded(raw string) map[string]bool {
	out := map[string]bool{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	var names []string
	_ = json.Unmarshal([]byte(raw), &names)
	for _, n := range names {
		out[n] = true
	}
	return out
}

// executeFunc builds the per-op ExecuteFunc closure. The op definition
// is captured by value at module-build time — edits to the row only
// take effect after an explicit Reload swaps the module (the design's
// no-hot-reload rule).
func (s *Service) executeFunc(cfgFields []DefField, op DefOp) connector.ExecuteFunc {
	cfgKeys := fieldKeys(cfgFields)
	inKeys := fieldKeys(op.Inputs)
	if op.MCPSource != nil {
		src := *op.MCPSource
		inputs := append([]DefField(nil), op.Inputs...)
		return func(c *connector.Ctx) (any, error) {
			return s.executeMCP(c, src, inputs)
		}
	}
	req := *op.Request
	return func(c *connector.Ctx) (any, error) {
		return executeHTTP(c, req, cfgKeys, inKeys)
	}
}

func fieldKeys(fields []DefField) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Key)
	}
	return out
}

// ctxMaps materializes the {.cfg, .in} template namespaces from the
// framework-resolved (already decrypted) Ctx reads.
func ctxMaps(c *connector.Ctx, cfgKeys, inKeys []string) (cfg, in map[string]string) {
	cfg = make(map[string]string, len(cfgKeys))
	for _, k := range cfgKeys {
		cfg[k] = c.Cfg(k)
	}
	in = make(map[string]string, len(inKeys))
	for _, k := range inKeys {
		in[k] = c.Input(k)
	}
	return cfg, in
}

// executeHTTP renders the stored request recipe and fires it through
// the shared client. Responses pass through as decoded JSON (or raw
// text when the upstream is not JSON); non-2xx statuses surface as
// errors carrying a body snippet for the history panel.
func executeHTTP(c *connector.Ctx, recipe OpRequest, cfgKeys, inKeys []string) (any, error) {
	cfg, in := ctxMaps(c, cfgKeys, inKeys)

	rurl, err := renderTemplate("url", recipe.URLTemplate, cfg, in)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(rurl, "http://") && !strings.HasPrefix(rurl, "https://") {
		return nil, fmt.Errorf("rendered URL %q is not http(s)", rurl)
	}

	var body io.Reader
	if recipe.BodyTemplate != "" {
		rendered, err := renderTemplate("body", recipe.BodyTemplate, cfg, in)
		if err != nil {
			return nil, err
		}
		body = strings.NewReader(rendered)
	}

	req, err := http.NewRequestWithContext(c.Context(), strings.ToUpper(recipe.Method), rurl, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for k, vt := range recipe.Headers {
		v, err := renderTemplate("header "+k, vt, cfg, in)
		if err != nil {
			return nil, err
		}
		req.Header.Set(k, v)
	}
	if recipe.ContentType != "" && body != nil {
		req.Header.Set("Content-Type", recipe.ContentType)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call %s %s: %w", recipe.Method, rurl, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponse))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, snippet(raw, 300))
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw), nil
	}
	return v, nil
}

// executeMCP proxies one tools/call to the op's registered MCP server.
// The server row is re-read per call so credential edits apply without
// a connector reload; the def's op list still requires Reload (the row
// only stores routing, not schema).
func (s *Service) executeMCP(c *connector.Ctx, src MCPSource, inputs []DefField) (any, error) {
	row, err := s.store.GetServer(c.Context(), src.ServerID)
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", src.ServerID, err)
	}
	srv, err := resolveServerConfig(row.URL, row.AuthScheme, row.AuthSecret, row.AuthHeaders, row.AuthExtra, row.Headers)
	if err != nil {
		return nil, err
	}

	var claims *SSOClaims
	if row.AuthScheme == "sso" {
		user := login.GetUser(c.Context())
		if user == nil {
			return nil, fmt.Errorf("sso-authenticated MCP call requires a logged-in caller")
		}
		claims = &SSOClaims{
			Subject: user.ID,
			Email:   user.Email,
			Name:    user.Name,
			Groups:  login.GetUserTagIDs(c.Context()),
		}
	}
	if row.AuthScheme == "oauth" {
		// Per-instance account: the calling instance's own token, with
		// transparent refresh through the server's OAuth client.
		meta := parseOAuthMeta(row.AuthExtra)
		tok, err := s.instanceAccessToken(c.Context(), &meta, c.InstanceID())
		if err != nil {
			return nil, err
		}
		if tok == "" {
			return nil, fmt.Errorf("no OAuth account connected to this instance — open its page and click Connect account")
		}
		srv.AccessToken = tok
	}

	client := s.mcp(c.HTTP)
	return client.Call(c.Context(), srv, src.ToolName, coerceArgs(inputs, c), claims)
}

// coerceArgs converts wick's flat string input map into the typed
// arguments an MCP inputSchema expects, using the stored widget as the
// type hint. Textarea fields holding JSON pass through as structured
// values so object/array parameters survive the round-trip.
//
// Outbound argument names are the SERVER's original property names
// (kept in Label by mapInputSchema), not wick's slugged keys — a
// camelCase parameter like libraryId slugs to libraryid for the wick
// input map, and sending the slug back fails the server's schema
// validation.
func coerceArgs(fields []DefField, c *connector.Ctx) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		raw := c.Input(f.Key)
		if raw == "" {
			continue
		}
		name := f.Label
		if name == "" {
			name = f.Key
		}
		switch f.Widget {
		case "number":
			if n, err := strconv.ParseFloat(raw, 64); err == nil {
				out[name] = n
				continue
			}
		case "checkbox", "bool":
			out[name] = raw == "true" || raw == "1" || raw == "yes" || raw == "on"
			continue
		case "textarea":
			if looksLikeJSON(raw) {
				var v any
				if err := json.Unmarshal([]byte(raw), &v); err == nil {
					out[name] = v
					continue
				}
			}
		}
		out[name] = raw
	}
	return out
}
