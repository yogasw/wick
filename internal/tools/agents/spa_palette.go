package agents

import (
	"net/http"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Palette tree is the only structural definition of the editor's "Add
// node" picker. Backend owns category + label + badge + drill structure
// so a new node type / channel / connector lights up the FE with zero
// hand-coded mapping. Routes:
//
//   GET /api/workflows/palette → full tree
//
// Shape:
//   {
//     categories: [
//       { key: "TRIGGERS", title: "TRIGGERS", items: [...] },
//       ...
//     ],
//     drills: {
//       "channel-ops:slack":     [ ... op items ... ],
//       "channel-trigger:slack": [ ... event items ... ],
//       "connector-ops:github":  [ ... op items ... ],
//       "datatable":             [ ... datatable items ... ],
//     }
//   }
//
// Each item is one of:
//   { kind:"drag", label, badge?, description?, drag: { type:"node",    nodeType, channel?, module?, op? } }
//   { kind:"drag", label, badge?, description?, drag: { type:"trigger", triggerType } }
//   { kind:"drag", label, badge?, description?, drag: { type:"channel-trigger", channel, event } }
//   { kind:"drill", label, badge?, drillKey }     // drillKey indexes into drills{}
//
// FE iterates `categories[].items[]`; when it hits a `drill` it
// navigates to `drills[drillKey]` and renders the same item shape.

type paletteItem struct {
	Kind        string         `json:"kind"` // "drag" | "drill"
	Label       string         `json:"label"`
	Badge       string         `json:"badge,omitempty"`
	Description string         `json:"description,omitempty"`
	Drag        map[string]any `json:"drag,omitempty"`
	DrillKey    string         `json:"drill_key,omitempty"`
}

type paletteCategory struct {
	Key   string        `json:"key"`
	Title string        `json:"title"`
	Items []paletteItem `json:"items"`
}

type paletteResponse struct {
	Categories []paletteCategory        `json:"categories"`
	Drills     map[string][]paletteItem `json:"drills"`
}

func registerSPAPalette(r tool.Router) {
	r.GET("/api/workflows/palette", spaWorkflowPalette)
}

// spaWorkflowPalette builds the tree on demand from the runtime
// registries. Read-only, but per-user: connector instances are filtered
// to the caller's accessible rows (owner + shared/mirror tags) exactly
// like the manager's connector list, so the palette never surfaces
// instances a user can't reach.
func spaWorkflowPalette(c *tool.Ctx) {
	if globalWorkflowMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "workflow manager not ready"})
		return
	}

	resp := buildPalette(gatherConnectorData(c))
	c.JSON(http.StatusOK, resp)
}

// connectorInstance is one accessible connector row plus its connected SSO
// accounts and the per-instance operation-enabled map. Precomputed in the
// handler so buildPalette stays a pure, testable transform.
type connectorInstance struct {
	Row      entity.Connector
	Accounts []entity.ConnectorAccount // SSO accounts (empty for non-SSO or none connected)
	// OpEnabled[opKey] reports whether the op is enabled on THIS instance
	// (admin per-row enable/disable + health-check, already resolved).
	// nil means "all enabled" (states unavailable — fail open).
	OpEnabled map[string]bool
}

// gatherConnectorData resolves the caller's accessible connector instances
// and their SSO accounts. Access = tags, exactly like the manager "wick
// list" (ListForManager: live filter tags, admins see all). No extra
// per-account gate — if the instance passes the tag filter, ALL its
// connected accounts are offered.
//
// Unlike the manager list, needs_setup instances are dropped: the palette
// is a surface for BUILDING runs, and an instance whose creds aren't fully
// configured can't execute, so dropping its node would only fail at run
// time. (An SSO instance is "ready" once its row config is complete; a
// missing connected account is fine — the row's Default credentials still
// run, and accounts are offered on top.)
func gatherConnectorData(c *tool.Ctx) map[string][]connectorInstance {
	out := map[string][]connectorInstance{}
	if globalConnectors == nil {
		return out
	}
	user := login.GetUser(c.Context())
	if user == nil {
		return out
	}
	var tagIDs []string
	if globalAuth != nil {
		tagIDs = globalAuth.GetUserFilterTagIDs(c.Context(), user.ID)
	}
	rows, err := globalConnectors.ListForManager(c.Context(), tagIDs, user.IsAdmin())
	if err != nil {
		return out
	}

	for _, row := range rows {
		if globalConnectors.Status(row) != "ready" {
			continue // needs_setup — can't run, so not droppable
		}
		inst := connectorInstance{Row: row}
		inst.OpEnabled, _ = globalConnectors.OperationStates(c.Context(), row.ID, row.Key)
		if row.EnableSSO {
			inst.Accounts, _ = globalConnectors.ListAccounts(c.Context(), row.ID)
		}
		out[row.Key] = append(out[row.Key], inst)
	}
	return out
}

// buildPalette assembles the palette tree from engine.Triggers,
// engine.Descriptors (via MCP.NodeTypes), the channel registry, and
// the connector registry. Separated from the handler so tests can
// exercise the shape without HTTP plumbing.
//
// instancesByKey maps connector key → the caller's accessible instances
// (already access-filtered, with readiness + usable SSO accounts resolved
// by gatherConnectorData). A connector with no accessible instance is
// omitted from the palette.
func buildPalette(instancesByKey map[string][]connectorInstance) paletteResponse {
	resp := paletteResponse{
		Categories: []paletteCategory{},
		Drills:     map[string][]paletteItem{},
	}

	// ── TRIGGERS ─────────────────────────────────────────────────
	triggers := []paletteItem{}
	if globalWorkflowMgr.Engine != nil && globalWorkflowMgr.Engine.Triggers != nil {
		for _, d := range globalWorkflowMgr.Engine.Triggers.List() {
			t := string(d.Type)
			if t == "channel" {
				continue // per-channel rows are added below as drills
			}
			triggers = append(triggers, paletteItem{
				Kind:        "drag",
				Label:       triggerLabel(t),
				Description: d.Description,
				Badge:       triggerBadge(t),
				Drag: map[string]any{
					"type":         "trigger",
					"trigger_type": t,
				},
			})
		}
	}
	// Channel-as-trigger drill rows — one per channel that exposes events.
	for _, ch := range globalWorkflowMgr.MCP.ChannelsList() {
		evs := globalWorkflowMgr.Integration.EventsByChannel(ch.Name)
		if len(evs) == 0 {
			continue
		}
		key := "channel-trigger:" + ch.Name
		events := make([]paletteItem, 0, len(evs))
		for _, ev := range evs {
			events = append(events, paletteItem{
				Kind:        "drag",
				Label:       ev.Name,
				Description: ev.Description,
				Drag: map[string]any{
					"type":    "channel-trigger",
					"channel": ch.Name,
					"event":   ev.Event,
				},
			})
		}
		resp.Drills[key] = events
		triggers = append(triggers, paletteItem{
			Kind:     "drill",
			Label:    titleizeSlug(ch.Name),
			Badge:    "trigger",
			DrillKey: key,
		})
	}
	if len(triggers) > 0 {
		resp.Categories = append(resp.Categories, paletteCategory{
			Key: "TRIGGERS", Title: "TRIGGERS", Items: triggers,
		})
	}

	// ── Node-type sourced categories (AI / ACTION / LOGIC / DATA) ──
	// One bucket per Category string returned by descriptors. Order is
	// deterministic via the bucketOrder slice; anything else gets sorted
	// after the known ones.
	byCat := map[string][]paletteItem{}
	for _, n := range globalWorkflowMgr.MCP.NodeTypes() {
		// Bare channel/connector node types are umbrellas — palette
		// shows per-channel / per-module drills instead. Skip them
		// here; their drills are emitted further down.
		if n.Type == "channel" || n.Type == "connector" {
			continue
		}
		// Datatable: 7 node types; we group them under one drill row
		// rather than splat them as siblings of HTTP/Shell/etc.
		if strings.HasPrefix(n.Type, "datatable_") {
			resp.Drills["datatable"] = append(resp.Drills["datatable"], paletteItem{
				Kind:        "drag",
				Label:       fallbackLabel(n.Label, n.Type),
				Badge:       n.Badge,
				Description: n.Description,
				Drag: map[string]any{
					"type":      "node",
					"node_type": n.Type,
				},
			})
			continue
		}
		cat := n.Category
		if cat == "" {
			cat = string(engine.CategoryAction)
		}
		byCat[cat] = append(byCat[cat], paletteItem{
			Kind:        "drag",
			Label:       fallbackLabel(n.Label, n.Type),
			Badge:       n.Badge,
			Description: n.Description,
			Drag: map[string]any{
				"type":      "node",
				"node_type": n.Type,
			},
		})
	}

	// Per-channel action drills — one drill per channel that exposes
	// at least one action. Same source the existing /workflows/api/registry
	// endpoint uses, so the palette stays consistent with the inspector.
	for _, ch := range globalWorkflowMgr.MCP.ChannelsList() {
		if len(ch.Actions) == 0 {
			continue
		}
		key := "channel-ops:" + ch.Name
		ops := make([]paletteItem, 0, len(ch.Actions))
		for _, a := range ch.Actions {
			ops = append(ops, paletteItem{
				Kind:        "drag",
				Label:       titleizeSlug(a.ID),
				Description: a.Description,
				Drag: map[string]any{
					"type":      "node",
					"node_type": "channel",
					"channel":   ch.Name,
					"op":        a.ID,
				},
			})
		}
		resp.Drills[key] = ops
		byCat[string(engine.CategoryAction)] = append(byCat[string(engine.CategoryAction)], paletteItem{
			Kind:     "drill",
			Label:    titleizeSlug(ch.Name),
			Badge:    "channel",
			DrillKey: key,
		})
	}
	// Per-connector drills. Levels: connector → [instance / SSO account] → op.
	// Access = tags (instancesByKey is already tag-filtered like wick list).
	// Each connector expands into a flat list of droppable ENTRIES:
	//   • non-SSO instance        → 1 entry (row_id)
	//   • SSO instance, N accounts → N entries (row_id + account_id), shown
	//                                even when N==1 so the user knows which
	//                                account runs
	//   • SSO instance, 0 accounts → 1 entry (row_id only; ops drill runs
	//                                with row-level creds) — mirrors wick
	//                                list still showing the row
	// Then: 0 entries → omit; 1 entry → straight to ops (skip picker);
	// ≥2 → picker drill (connector-instances:<module>), each entry drills
	// to its own ops (connector-ops:<module>:<entryKey>).
	for _, info := range globalWorkflowMgr.MCP.ConnectorsList() {
		mod, ok := globalWorkflowMgr.Connectors.Module(info.Module)
		if !ok {
			continue
		}
		modOps := mod.AllOps()
		if len(modOps) == 0 {
			continue
		}
		insts := instancesByKey[info.Module]
		if len(insts) == 0 {
			continue // caller has no accessible instance for this connector
		}

		// opsFor builds the op drag items for one entry, pinning row_id and
		// (optionally) account_id. Ops disabled on this instance (enabled,
		// nil = all enabled) or disabled for this account (accDisabled) are
		// omitted — a node the user can't run shouldn't be droppable.
		opsFor := func(rowID, accountID string, enabled, accDisabled map[string]bool) []paletteItem {
			ops := make([]paletteItem, 0, len(modOps))
			for _, op := range modOps {
				if enabled != nil && !enabled[op.Key] {
					continue // admin-disabled / health-check-disabled on this instance
				}
				if accDisabled[op.Key] {
					continue // disabled for this specific account
				}
				drag := map[string]any{
					"type":      "node",
					"node_type": "connector",
					"module":    info.Module,
					"op":        op.Key,
					"row_id":    rowID,
				}
				if accountID != "" {
					drag["account_id"] = accountID
				}
				ops = append(ops, paletteItem{
					Kind:        "drag",
					Label:       op.Name,
					Description: op.Description,
					Drag:        drag,
				})
			}
			return ops
		}

		type entry struct {
			label  string
			opsKey string
		}
		var entries []entry
		for _, inst := range insts {
			base := inst.Row.Label
			if strings.TrimSpace(base) == "" {
				base = info.Name
			}
			// Row-level creds ("Default credentials") — ALWAYS available,
			// even on an SSO instance: connectors like Slack work with a
			// static token OR an SSO account, so both are offered. Mirrors
			// the manager test panel's "Run as: [Default credentials, @…]".
			rowOps := opsFor(inst.Row.ID, "", inst.OpEnabled, nil)
			if len(rowOps) > 0 {
				rowOpsKey := "connector-ops:" + info.Module + ":" + inst.Row.ID
				resp.Drills[rowOpsKey] = rowOps
				label := base
				if inst.Row.EnableSSO && len(inst.Accounts) > 0 {
					// Disambiguate from the account entries below.
					label = base + " · Default credentials"
				}
				entries = append(entries, entry{label: label, opsKey: rowOpsKey})
			}

			// One entry per connected SSO account (row_id + account_id).
			// Skip an account with no runnable op left.
			for _, acc := range inst.Accounts {
				accCopy := acc
				accDisabled := connectors.AccountDisabledOps(&accCopy)
				accOps := opsFor(inst.Row.ID, acc.ID, inst.OpEnabled, accDisabled)
				if len(accOps) == 0 {
					continue
				}
				opsKey := "connector-ops:" + info.Module + ":" + inst.Row.ID + ":" + acc.ID
				resp.Drills[opsKey] = accOps
				entries = append(entries, entry{label: base + " · @" + acc.DisplayName, opsKey: opsKey})
			}
		}

		if len(entries) == 0 {
			continue
		}

		// One entry → skip the picker, drill straight to its ops.
		if len(entries) == 1 {
			key := "connector-ops:" + info.Module
			resp.Drills[key] = resp.Drills[entries[0].opsKey]
			delete(resp.Drills, entries[0].opsKey)
			byCat[string(engine.CategoryAction)] = append(byCat[string(engine.CategoryAction)], paletteItem{
				Kind:     "drill",
				Label:    info.Name,
				Badge:    "connector",
				DrillKey: key,
			})
			continue
		}

		// ≥2 entries: picker drill, each entry drills to its own ops.
		instKey := "connector-instances:" + info.Module
		instItems := make([]paletteItem, 0, len(entries))
		for _, e := range entries {
			instItems = append(instItems, paletteItem{
				Kind:     "drill",
				Label:    e.label,
				DrillKey: e.opsKey,
			})
		}
		sortEntryItems(instItems)
		resp.Drills[instKey] = instItems
		byCat[string(engine.CategoryAction)] = append(byCat[string(engine.CategoryAction)], paletteItem{
			Kind:     "drill",
			Label:    info.Name,
			Badge:    "connector",
			DrillKey: instKey,
		})
	}
	// Data Tables drill — surfaced under DATA only when we collected
	// at least one datatable_* node descriptor above.
	if len(resp.Drills["datatable"]) > 0 {
		byCat[string(engine.CategoryData)] = append(byCat[string(engine.CategoryData)], paletteItem{
			Kind:     "drill",
			Label:    "Data Tables",
			Badge:    "table ops",
			DrillKey: "datatable",
		})
	}

	bucketOrder := []string{
		string(engine.CategoryAI),
		string(engine.CategoryAction),
		string(engine.CategoryLogic),
		string(engine.CategoryData),
	}
	known := map[string]bool{}
	for _, k := range bucketOrder {
		known[k] = true
	}
	for _, k := range bucketOrder {
		items := byCat[k]
		if len(items) == 0 {
			continue
		}
		sortPaletteItems(items)
		resp.Categories = append(resp.Categories, paletteCategory{
			Key: k, Title: k, Items: items,
		})
	}
	// Unknown categories (custom node packs) at the end, sorted alpha.
	others := []string{}
	for k := range byCat {
		if !known[k] {
			others = append(others, k)
		}
	}
	sort.Strings(others)
	for _, k := range others {
		items := byCat[k]
		sortPaletteItems(items)
		resp.Categories = append(resp.Categories, paletteCategory{
			Key: k, Title: k, Items: items,
		})
	}

	// Sort drill contents so the order is stable across requests.
	// connector-instances:* lists are already ordered by sortEntryItems
	// (droppable-first) and must not be re-sorted alpha here.
	for k := range resp.Drills {
		if strings.HasPrefix(k, "connector-instances:") {
			continue
		}
		sortPaletteItems(resp.Drills[k])
	}
	return resp
}

// sortEntryItems orders connector instance/account picker rows: droppable
// entries (kind "drill") first, needs-connect placeholders (kind "drag"
// with no Drag payload) last, then alphabetically by label. Stable so
// equal-rank rows keep insertion order.
func sortEntryItems(items []paletteItem) {
	sort.SliceStable(items, func(i, j int) bool {
		di, dj := items[i].Kind == "drill", items[j].Kind == "drill"
		if di != dj {
			return di // droppable first
		}
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})
}

// fallbackLabel returns the descriptor's explicit label, or a
// title-cased version of the type slug if the descriptor didn't set
// one. Mirrors the old FE prettyLabel() so behaviour stays consistent
// for executors that don't yet declare Label.
func fallbackLabel(label, slug string) string {
	if strings.TrimSpace(label) != "" {
		return label
	}
	return titleizeSlug(slug)
}

// titleizeSlug turns "send_message" into "Send Message". Identical
// semantics to the FE's `slug.replace(/_/g, " ").replace(/\b./, …)`.
func titleizeSlug(s string) string {
	if s == "" {
		return ""
	}
	out := make([]rune, 0, len(s))
	upperNext := true
	for _, r := range s {
		if r == '_' || r == '-' {
			out = append(out, ' ')
			upperNext = true
			continue
		}
		if upperNext {
			if r >= 'a' && r <= 'z' {
				r = r - 'a' + 'A'
			}
			upperNext = false
		}
		out = append(out, r)
	}
	return string(out)
}

// triggerBadge maps a trigger type to its short right-aligned hint.
// Kept here (not on TriggerDescriptor) because trigger descriptors
// already encode Schema + Docs; adding a UI badge field there would
// pollute the discovery contract. Only known trigger types get a
// badge; everything else returns "".
func triggerBadge(t string) string {
	switch t {
	case "error":
		return "on fail"
	case "cron":
		return "schedule"
	case "webhook":
		return "HTTP POST"
	case "manual":
		return "button"
	case "schedule_at":
		return "one-shot"
	}
	return ""
}

// sortPaletteItems gives drill rows priority (so the parent comes
// before its leaf siblings — matches v1) and otherwise sorts alpha by
// label. Stable so equal-rank items keep their insertion order.
func sortPaletteItems(items []paletteItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "drag" && items[j].Kind == "drill" // drag first, drill last
		}
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})
}
