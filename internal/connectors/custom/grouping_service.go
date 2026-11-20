package custom

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// GroupingState is what grouping_get returns for one MCP definition.
type GroupingState struct {
	Key      string      `json:"key"`
	ServerID string      `json:"server_id"`
	Rules    *GroupRules `json:"rules"`
}

// mcpDefByKey loads an MCP-backed definition and its server row by key.
func (s *Service) mcpDefByKey(ctx context.Context, key string) (*entity.CustomConnector, *entity.CustomConnectorMCPServer, error) {
	defID, ok := s.DefIDForKey(key)
	if !ok {
		return nil, nil, errors.New("custom connector not found: " + key)
	}
	def, err := s.store.GetDef(ctx, defID)
	if err != nil {
		return nil, nil, err
	}
	if def.Source != entity.CustomConnectorSourceMCP {
		return nil, nil, errors.New("grouping rules apply to MCP-backed custom connectors only; other definitions set sections in their ops")
	}
	serverID := ParseSourceMeta(def.SourceMeta).ServerID
	if serverID == "" {
		return nil, nil, errors.New("definition has no MCP server")
	}
	row, err := s.store.GetServer(ctx, serverID)
	if err != nil {
		return nil, nil, err
	}
	return def, row, nil
}

// GroupingGet returns the stored recipe of an MCP definition (nil rules
// when none is set).
func (s *Service) GroupingGet(ctx context.Context, key string) (GroupingState, error) {
	_, row, err := s.mcpDefByKey(ctx, key)
	if err != nil {
		return GroupingState{}, err
	}
	rules, err := ParseGroupRules(row.GroupRules)
	if err != nil {
		return GroupingState{}, err
	}
	return GroupingState{Key: key, ServerID: row.ID, Rules: rules}, nil
}

// liveTools reads the server's current tools for a preview or a
// suggestion, ungrouped, with each tool's own _meta.category.
func (s *Service) liveTools(ctx context.Context, key, preferInstance string) ([]PreviewTool, error) {
	def, _, err := s.mcpDefByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	live, ok := s.liveMCPRaw(ctx, def, preferInstance)
	if !ok {
		return nil, errors.New("could not read the server's tools/list (see the connector status)")
	}
	out := make([]PreviewTool, 0, len(live.ops))
	for _, op := range live.ops {
		name := op.Key
		if op.MCPSource != nil {
			name = op.MCPSource.ToolName
		}
		out = append(out, PreviewTool{Name: name, Description: op.Description, Category: op.Category})
	}
	return out, nil
}

// GroupingPreview dry-runs rules (the stored ones when rules is nil)
// against the live tool list. Nothing is saved.
func (s *Service) GroupingPreview(ctx context.Context, key, preferInstance string, rules *GroupRules) (GroupPreview, error) {
	if rules == nil {
		st, err := s.GroupingGet(ctx, key)
		if err != nil {
			return GroupPreview{}, err
		}
		if st.Rules == nil {
			rules = &GroupRules{}
		} else {
			rules = st.Rules
		}
	}
	tools, err := s.liveTools(ctx, key, preferInstance)
	if err != nil {
		return GroupPreview{}, err
	}
	return PreviewGroups(tools, *rules)
}

// GroupingSuggest guesses a recipe from the live tool list and previews it.
func (s *Service) GroupingSuggest(ctx context.Context, key, preferInstance string) (GroupRules, GroupPreview, error) {
	tools, err := s.liveTools(ctx, key, preferInstance)
	if err != nil {
		return GroupRules{}, GroupPreview{}, err
	}
	g := SuggestGroupRules(tools)
	p, err := PreviewGroups(tools, g)
	return g, p, err
}

// GroupingSet validates and stores a recipe, then rebuilds the module so
// the new sections are live. A nil rules clears the recipe.
func (s *Service) GroupingSet(ctx context.Context, key string, rules *GroupRules) error {
	def, row, err := s.mcpDefByKey(ctx, key)
	if err != nil {
		return err
	}
	raw := ""
	if rules != nil {
		if err := rules.Validate(); err != nil {
			return err
		}
		b, err := json.Marshal(rules)
		if err != nil {
			return err
		}
		raw = string(b)
		if strings.TrimSpace(raw) == "{}" {
			raw = ""
		}
	}
	row.GroupRules = raw
	if err := s.store.UpdateServer(ctx, row); err != nil {
		return err
	}
	return s.Reload(ctx, def.ID)
}
