package customconnector

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
	custom "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
)

// parseRulesInput decodes the optional rules JSON; "" is nil.
func parseRulesInput(raw string) (*custom.GroupRules, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	g, err := custom.ParseGroupRules(raw)
	if err != nil {
		return nil, err
	}
	if g == nil {
		g = &custom.GroupRules{}
	}
	return g, nil
}

func (h handlers) groupingDef(c *connector.Ctx) (*entity.CustomConnector, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, err
	}
	def, err := h.mutableDef(c.Context(), user, c.Input("key"))
	if err != nil {
		return nil, err
	}
	if custom.ServerIDForDef(def) == "" {
		return nil, errNotMCP
	}
	return def, nil
}

func (h handlers) groupingGet(c *connector.Ctx) (any, error) {
	def, err := h.groupingDef(c)
	if err != nil {
		return nil, err
	}
	return h.deps.Custom.GroupingGet(c.Context(), def.Key)
}

func (h handlers) groupingSuggest(c *connector.Ctx) (any, error) {
	def, err := h.groupingDef(c)
	if err != nil {
		return nil, err
	}
	rules, preview, err := h.deps.Custom.GroupingSuggest(c.Context(), def.Key, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"rules": rules, "preview": preview}, nil
}

func (h handlers) groupingPreview(c *connector.Ctx) (any, error) {
	def, err := h.groupingDef(c)
	if err != nil {
		return nil, err
	}
	rules, err := parseRulesInput(c.Input("rules"))
	if err != nil {
		return nil, err
	}
	return h.deps.Custom.GroupingPreview(c.Context(), def.Key, "", rules)
}

func (h handlers) groupingSet(c *connector.Ctx) (any, error) {
	def, err := h.groupingDef(c)
	if err != nil {
		return nil, err
	}
	rules, err := parseRulesInput(c.Input("rules"))
	if err != nil {
		return nil, err
	}
	if err := h.deps.Custom.GroupingSet(c.Context(), def.Key, rules); err != nil {
		return nil, err
	}
	var groups []string
	if mod, ok := h.deps.Connectors.Module(def.Key); ok {
		for _, cat := range mod.Operations {
			groups = append(groups, fmt.Sprintf("%s (%d)", cat.Title, len(cat.Ops)))
		}
	}
	return map[string]any{"ok": true, "key": def.Key, "groups": groups}, nil
}

// opInstance loads a connector instance the caller can see.
func (h handlers) opInstance(c *connector.Ctx) (*entity.User, *entity.Connector, connector.Module, error) {
	user, err := requireUser(c.Context())
	if err != nil {
		return nil, nil, connector.Module{}, err
	}
	id := strings.TrimSpace(c.Input("connector_id"))
	ok, err := h.deps.Connectors.IsVisibleTo(c.Context(), id, user.ID, login.GetUserTagIDs(c.Context()), user.IsAdmin())
	if err != nil || !ok {
		return nil, nil, connector.Module{}, errors.New("connector not found or not accessible")
	}
	row, err := h.deps.Connectors.Get(c.Context(), id)
	if err != nil {
		return nil, nil, connector.Module{}, err
	}
	mod, found := h.deps.Connectors.Module(row.Key)
	if !found {
		return nil, nil, connector.Module{}, errors.New("connector module not registered")
	}
	return user, row, mod, nil
}

func (h handlers) opPrefsList(c *connector.Ctx) (any, error) {
	_, row, mod, err := h.opInstance(c)
	if err != nil {
		return nil, err
	}
	ctx := c.Context()
	prefs := h.deps.Connectors.OpPrefs(ctx, row.ID)
	ranking := h.deps.Connectors.UsageRanking(ctx, "", row.ID)
	calls := map[string]int{}
	for _, u := range ranking {
		calls[u.OperationKey] = u.Calls
	}
	type opRow struct {
		Key         string `json:"key"`
		Destructive bool   `json:"destructive,omitempty"`
		Calls30d    int    `json:"calls_30d"`
		Pinned      bool   `json:"pinned,omitempty"`
		Muted       bool   `json:"muted,omitempty"`
		Note        string `json:"note,omitempty"`
		Keywords    string `json:"keywords,omitempty"`
	}
	exists := map[string]bool{}
	var ops []opRow
	for _, op := range mod.AllOps() {
		if op.ConfigOnly {
			continue
		}
		exists[op.Key] = true
		p := prefs[row.ID+"/"+op.Key]
		ops = append(ops, opRow{Key: op.Key, Destructive: op.Destructive, Calls30d: calls[op.Key],
			Pinned: p.Pinned, Muted: p.Muted, Note: p.Note, Keywords: p.Keywords})
	}
	var orphaned []string
	for _, p := range prefs {
		if !exists[p.OperationKey] {
			orphaned = append(orphaned, p.OperationKey)
		}
	}
	var frequent []string
	for _, f := range connectors.PickFrequent(mod, row.ID, ranking, prefs, nil, connectors.FrequentPerConnector) {
		frequent = append(frequent, connectors.Signature(f.Op))
	}
	return map[string]any{"connector_id": row.ID, "key": row.Key, "frequent": frequent, "ops": ops, "orphaned": orphaned}, nil
}

func (h handlers) opPrefSet(c *connector.Ctx) (any, error) {
	user, row, mod, err := h.opInstance(c)
	if err != nil {
		return nil, err
	}
	if !user.IsAdmin() {
		defID, ok := h.deps.Custom.DefIDForKey(row.Key)
		if !ok {
			return nil, errors.New("only an admin can curate ops of a built-in connector")
		}
		def, err := h.deps.Custom.Store().GetDef(c.Context(), defID)
		if err != nil || !custom.CanMutate(def, user) {
			return nil, errors.New("only an admin or the connector's creator can curate its ops")
		}
	}
	opKey := strings.TrimSpace(c.Input("op_key"))
	found := false
	for _, op := range mod.AllOps() {
		if op.Key == opKey {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("unknown operation %q on %s", opKey, row.Key)
	}
	var patch connectors.OpPrefPatch
	boolOf := func(s string) *bool {
		switch strings.TrimSpace(s) {
		case "true":
			v := true
			return &v
		case "false":
			v := false
			return &v
		}
		return nil
	}
	patch.Pinned = boolOf(c.Input("pinned"))
	patch.Muted = boolOf(c.Input("muted"))
	strOf := func(s string) *string {
		s = strings.TrimSpace(s)
		switch s {
		case "":
			return nil
		case "-":
			e := ""
			return &e
		}
		return &s
	}
	patch.Note = strOf(c.Input("note"))
	patch.Keywords = strOf(c.Input("keywords"))
	p, err := h.deps.Connectors.SetOpPref(c.Context(), row.ID, opKey, user.ID, patch)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(p)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	delete(out, "UpdatedBy")
	return out, nil
}
