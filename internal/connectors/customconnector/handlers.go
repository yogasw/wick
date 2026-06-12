package customconnector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	custom "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
)

var (
	errNotAuthenticated = errors.New("not authenticated")
	errNotCustom        = errors.New("no custom connector definition with that key (or it is not yours to manage)")
	errNotMCP           = errors.New("definition is not MCP-sourced")
)

// handlers binds every op to the management services. All state lives
// in the services — the struct only carries the wiring.
type handlers struct {
	deps Deps
}

// requireUser pulls the authenticated caller off the context (the MCP
// tools/call middleware always populates it). Authorization is scoped,
// not admin-gated: admins manage every definition, everyone else only
// the ones they created — mirroring how wick_list scopes by tags.
func requireUser(ctx context.Context) (*entity.User, error) {
	u := login.GetUser(ctx)
	if u == nil {
		return nil, errNotAuthenticated
	}
	return u, nil
}

// mutableDef resolves a key to a definition the caller may manage
// (admin ∨ creator). Missing and not-yours are indistinguishable on
// purpose.
func (h handlers) mutableDef(ctx context.Context, user *entity.User, key string) (*entity.CustomConnector, error) {
	defID, ok := h.deps.Custom.DefIDForKey(strings.TrimSpace(key))
	if !ok {
		return nil, errNotCustom
	}
	def, err := h.deps.Custom.Store().GetDef(ctx, defID)
	if err != nil {
		return nil, err
	}
	if !custom.CanMutate(def, user) {
		return nil, errNotCustom
	}
	return def, nil
}

// ── definitions ──────────────────────────────────────────────────────

func (h handlers) defList(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	defs, err := h.deps.Custom.Store().ListDefs(c.Context())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		// Caller scoping: admins list everything, everyone else only
		// the definitions they created.
		if !custom.CanMutate(&def, user) {
			continue
		}
		entry := map[string]any{
			"key":             def.Key,
			"name":            def.Name,
			"source":          string(def.Source),
			"disabled":        def.Disabled,
			"single_instance": def.SingleInstance,
		}
		if mod, ok := h.deps.Connectors.Module(def.Key); ok {
			entry["operations"] = len(mod.Operations)
		}
		if rows, err := h.deps.Connectors.ListByKey(c.Context(), def.Key); err == nil {
			entry["instances"] = len(rows)
		}
		out = append(out, entry)
	}
	return map[string]any{"definitions": out}, nil
}

func (h handlers) defGet(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	fields, err := custom.ParseFields(def.Configs)
	if err != nil {
		return nil, err
	}
	ops, err := custom.ParseOps(def.Ops)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"key":         def.Key,
		"name":        def.Name,
		"description": def.Description,
		"icon":        def.Icon,
		"source":      string(def.Source),
		"category":    custom.ParseSourceMeta(def.SourceMeta).Category,
		"single":      def.SingleInstance,
		"disabled":    def.Disabled,
		"configs":     fields,
		"ops":         ops,
	}
	if serverID := custom.ServerIDForDef(def); serverID != "" {
		srv, err := h.deps.Custom.Store().GetServer(c.Context(), serverID)
		if err == nil {
			excluded := []string{}
			if strings.TrimSpace(srv.ExcludedTools) != "" {
				_ = json.Unmarshal([]byte(srv.ExcludedTools), &excluded)
			}
			entry := map[string]any{
				"url":          srv.URL,
				"auth_scheme":  srv.AuthScheme,
				"excluded":     excluded,
				"last_test_ok": srv.LastTestOK,
			}
			// serverInfo from the last initialize — admin-facing detail
			// (name/version); wick_list deliberately never carries it.
			if strings.TrimSpace(srv.ServerInfo) != "" {
				var si map[string]string
				if err := json.Unmarshal([]byte(srv.ServerInfo), &si); err == nil {
					entry["server_info"] = si
				}
			}
			out["mcp_server"] = entry
		}
		// The live catalog is the module, not the (empty) ops column.
		if mod, ok := h.deps.Connectors.Module(def.Key); ok {
			names := make([]string, 0, len(mod.Operations))
			for _, op := range mod.Operations {
				names = append(names, op.Key)
			}
			out["ops"] = names
		}
	}
	return out, nil
}

func (h handlers) defCreate(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	var d custom.Draft
	if err := json.Unmarshal([]byte(c.Input("draft")), &d); err != nil {
		return nil, fmt.Errorf("draft is not valid JSON: %w", err)
	}
	if d.Source == "" {
		d.Source = "manual"
	}
	def, _, err := h.deps.Custom.SaveNew(c.Context(), &d, user.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"key": def.Key, "name": def.Name}, nil
}

func (h handlers) defUpdate(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	var d custom.Draft
	if err := json.Unmarshal([]byte(c.Input("draft")), &d); err != nil {
		return nil, fmt.Errorf("draft is not valid JSON: %w", err)
	}
	if err := h.deps.Custom.Update(c.Context(), def.ID, &d); err != nil {
		return nil, err
	}
	// The UI leaves reload as an explicit step (dirty banner); over MCP
	// the caller expects the edit live immediately.
	if err := h.deps.Custom.Reload(c.Context(), def.ID); err != nil {
		return nil, fmt.Errorf("saved but reload failed: %w", err)
	}
	return map[string]any{"ok": true, "key": def.Key}, nil
}

func (h handlers) defSetDisabled(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	disabled := c.InputBool("disabled")
	if err := h.deps.Custom.SetDefDisabled(c.Context(), def.ID, disabled); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "key": def.Key, "disabled": disabled}, nil
}

func (h handlers) defDelete(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	if err := h.deps.Custom.Delete(c.Context(), def.ID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "key": def.Key}, nil
}

func (h handlers) defResync(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	if err := h.deps.Custom.ReloadFor(c.Context(), def.ID, strings.TrimSpace(c.Input("instance_id"))); err != nil {
		return nil, err
	}
	count := 0
	if mod, ok := h.deps.Connectors.Module(def.Key); ok {
		count = len(mod.Operations)
	}
	return map[string]any{"ok": true, "key": def.Key, "operations": count}, nil
}

// ── MCP servers ──────────────────────────────────────────────────────

func (h handlers) mcpRegister(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	scheme := strings.TrimSpace(c.Input("auth_scheme"))
	if scheme == "" {
		scheme = "none"
	}
	if scheme == "oauth" {
		return nil, fmt.Errorf("the oauth scheme needs a browser login — register this server from the dashboard (+ New connector → From MCP server)")
	}
	form := &custom.ServerForm{
		Label:       strings.TrimSpace(c.Input("label")),
		Icon:        strings.TrimSpace(c.Input("icon")),
		Description: strings.TrimSpace(c.Input("description")),
		URL:         strings.TrimSpace(c.Input("url")),
		AuthScheme:  scheme,
		AuthSecret:  c.Input("auth_secret"),
	}
	if headers := strings.TrimSpace(c.Input("headers")); headers != "" {
		if err := json.Unmarshal([]byte(headers), &form.Headers); err != nil {
			return nil, fmt.Errorf("headers is not a valid JSON array of {key,value,secret}: %w", err)
		}
	}
	if excludedRaw := strings.TrimSpace(c.Input("excluded")); excludedRaw != "" {
		if err := json.Unmarshal([]byte(excludedRaw), &form.Excluded); err != nil {
			return nil, fmt.Errorf("excluded is not a valid JSON array of strings: %w", err)
		}
	}
	// The save gate: one successful initialize + tools/list with these
	// exact values, exactly like Test now on the form.
	probe := h.deps.Custom.TestServer(c.Context(), form, h.ssoClaims(user, c))
	if !probe.OK {
		return nil, fmt.Errorf("connection test failed: %s", probe.Error)
	}
	_, key, _, err := h.deps.Custom.SaveServer(c.Context(), form, true, "", user.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"key": key, "name": form.Label, "tools": len(probe.Tools)}, nil
}

// ssoClaims forwards the calling admin's identity for sso-scheme
// probes, mirroring the manager handler.
func (h handlers) ssoClaims(user *entity.User, c *connector.Ctx) *custom.SSOClaims {
	return &custom.SSOClaims{
		Subject: user.ID,
		Email:   user.Email,
		Name:    user.Name,
		Groups:  login.GetUserTagIDs(c.Context()),
	}
}

func (h handlers) mcpSetExcluded(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	serverID := custom.ServerIDForDef(def)
	if serverID == "" {
		return nil, errNotMCP
	}
	excluded := []string{}
	if err := json.Unmarshal([]byte(c.Input("excluded")), &excluded); err != nil {
		return nil, fmt.Errorf("excluded is not a valid JSON array of strings: %w", err)
	}
	srv, err := h.deps.Custom.Store().GetServer(c.Context(), serverID)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(excluded)
	srv.ExcludedTools = string(raw)
	if err := h.deps.Custom.Store().UpdateServer(c.Context(), srv); err != nil {
		return nil, err
	}
	if err := h.deps.Custom.Reload(c.Context(), def.ID); err != nil {
		return nil, fmt.Errorf("saved but re-sync failed: %w", err)
	}
	count := 0
	if mod, ok := h.deps.Connectors.Module(def.Key); ok {
		count = len(mod.Operations)
	}
	return map[string]any{"ok": true, "key": def.Key, "excluded": excluded, "operations": count}, nil
}

// ── instances ────────────────────────────────────────────────────────

func (h handlers) instanceList(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	rows, err := h.deps.Connectors.ListByKey(c.Context(), def.Key)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id":       row.ID,
			"label":    row.Label,
			"disabled": row.Disabled,
			"status":   h.deps.Connectors.Status(row),
		})
	}
	return map[string]any{"key": def.Key, "instances": out}, nil
}

func (h handlers) instanceCreate(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	label := strings.TrimSpace(c.Input("label"))
	if label == "" {
		label = def.Name + " (new)"
	}
	row, err := h.deps.Connectors.Create(c.Context(), def.Key, label, map[string]string{}, user.ID)
	if err != nil {
		return nil, err
	}
	// Same follow-up as the UI's "+ New row": link the per-def access
	// tags so the fresh row is governed immediately, and mark level-2
	// ownership for non-admin creators.
	h.deps.Custom.EnsureTagsForKey(c.Context(), def.Key)
	if !user.IsAdmin() {
		h.deps.Custom.TagInstanceOwner(c.Context(), row.ID, user.ID)
	}
	return map[string]any{"id": row.ID, "label": row.Label}, nil
}

// instanceRow loads a row, asserts it belongs to a CUSTOM def (this
// connector must not become a side door into built-in rows), and
// applies caller scoping: the def must be mutable by the caller.
func (h handlers) instanceRow(ctx context.Context, user *entity.User, id string) (*entity.Connector, error) {
	row, err := h.deps.Connectors.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if _, ok := h.deps.Custom.DefIDForKey(row.Key); !ok {
		return nil, fmt.Errorf("instance %s belongs to built-in connector %q — manage it via wickmanager or the dashboard", row.ID, row.Key)
	}
	if _, err := h.mutableDef(ctx, user, row.Key); err != nil {
		return nil, err
	}
	return row, nil
}

func (h handlers) instanceDelete(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	row, err := h.instanceRow(c.Context(), user, c.Input("instance_id"))
	if err != nil {
		return nil, err
	}
	if err := h.deps.Connectors.Delete(c.Context(), row.ID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": row.ID}, nil
}

func (h handlers) instanceSetDisabled(c *connector.Ctx) (any, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	row, err := h.instanceRow(c.Context(), user, c.Input("instance_id"))
	if err != nil {
		return nil, err
	}
	disabled := c.InputBool("disabled")
	if err := h.deps.Connectors.SetDisabled(c.Context(), row.ID, disabled); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": row.ID, "disabled": disabled}, nil
}
