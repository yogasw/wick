package setup

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
)

func TestNewNodeTypesInCatalog(t *testing.T) {
	m := New(config.NewLayout(t.TempDir()))
	cat := mcp.NodeTypesCatalog(m.Engine)
	want := map[string]bool{
		string(workflow.NodeGoScript): false,
		string(workflow.NodeSwitch):   false,
	}
	for _, n := range cat {
		if _, ok := want[n.Type]; ok {
			want[n.Type] = true
			if n.Description == "" {
				t.Errorf("%s: empty description", n.Type)
			}
			if len(n.Schema) == 0 {
				t.Errorf("%s: empty schema", n.Type)
			}
		}
	}
	for t2, found := range want {
		if !found {
			t.Errorf("%s missing from workflow_node_types catalog", t2)
		}
	}
}
