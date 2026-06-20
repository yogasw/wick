package manager

import (
	"net/http"
	"sort"

	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tags"
)

// connectorDef is the JSON shape served at GET /manager/api/connectors.
// It is the read model the manager SPA consumes to render the Connectors
// index: one entry per registered connector definition the caller may
// see, mirroring the cards built by connectorsIndexPage — including the
// per-definition description, operation count, and managed-instance state
// counts the card shows.
type connectorDef struct {
	Key             string `json:"key"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Category        string `json:"category"`
	CategoryDesc    string `json:"category_desc"`
	Icon            string `json:"icon"`
	OpCount         int    `json:"op_count"`
	ActiveCount     int    `json:"active_count"`
	NeedsSetupCount int    `json:"needs_setup_count"`
	DisabledCount   int    `json:"disabled_count"`
	System          bool   `json:"system"`
	Custom          bool   `json:"custom"`
	CustomSource    string `json:"custom_source"`
	NeedsReload     bool   `json:"needs_reload"`
	Disabled        bool   `json:"disabled"`
}

// apiConnectors serves the connector-definition catalog as JSON for the
// manager SPA. Visibility matches connectorsIndexPage: System defs are
// admin-only, and non-admins only see defs they manage at least one row
// of. The result is sorted by category sort order then name for a stable
// render order.
func (h *Handler) apiConnectors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	isAdmin := user != nil && user.IsAdmin()

	rows, err := h.connectors.ListForManager(ctx, login.GetUserTagIDs(ctx), isAdmin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Per-def instance counts, scoped to the rows the caller can manage.
	// Mirrors connectorsIndexPage so the "disabled" flag matches the card:
	// a def is shown disabled when every managed instance is row-disabled.
	type instanceCount struct{ active, needsSetup, disabled int }
	countByKey := make(map[string]instanceCount, len(rows))
	for _, row := range rows {
		c := countByKey[row.Key]
		switch {
		case row.Disabled:
			c.disabled++
		case h.connectors.Status(row) == "needs_setup":
			c.needsSetup++
		default:
			c.active++
		}
		countByKey[row.Key] = c
	}

	type defWithSort struct {
		def     connectorDef
		catSort int
	}
	out := make([]defWithSort, 0, len(rows))
	for _, m := range h.connectors.Modules() {
		system := hasDefaultTag(m.Meta.DefaultTags, tags.System.Name)
		if system && !isAdmin {
			continue
		}
		cnt := countByKey[m.Meta.Key]
		if !isAdmin && cnt.active+cnt.needsSetup+cnt.disabled == 0 {
			continue
		}
		cat, catSort, catDesc := connectorCategory(m.Meta.DefaultTags, system)
		def := connectorDef{
			Key:             m.Meta.Key,
			Name:            m.Meta.Name,
			Description:     m.Meta.Description,
			Category:        cat,
			CategoryDesc:    catDesc,
			Icon:            m.Meta.Icon,
			OpCount:         len(m.AllOps()),
			ActiveCount:     cnt.active,
			NeedsSetupCount: cnt.needsSetup,
			DisabledCount:   cnt.disabled,
			System:          system,
			Disabled:        cnt.disabled > 0 && cnt.active == 0 && cnt.needsSetup == 0,
		}
		if info := h.customDefInfo(ctx, m.Meta.Key, user); info != nil {
			def.Custom = true
			def.CustomSource = info.SourceLabel
			def.NeedsReload = info.Dirty
		}
		out = append(out, defWithSort{def: def, catSort: catSort})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].catSort != out[j].catSort {
			return out[i].catSort < out[j].catSort
		}
		if out[i].def.Category != out[j].def.Category {
			return out[i].def.Category < out[j].def.Category
		}
		return out[i].def.Name < out[j].def.Name
	})

	defs := make([]connectorDef, 0, len(out))
	for _, d := range out {
		defs = append(defs, d.def)
	}
	writeJSON(w, http.StatusOK, defs)
}
