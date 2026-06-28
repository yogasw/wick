package manager

import (
	"testing"

	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/tool"
)

func TestConnectorCategory(t *testing.T) {
	cases := []struct {
		name     string
		tags     []tool.DefaultTag
		system   bool
		wantName string
		wantSort int
	}{
		{
			name:     "category tag after connector umbrella",
			tags:     []tool.DefaultTag{tags.Connector, tags.Communication},
			wantName: "Communication",
			wantSort: tags.Communication.SortOrder,
		},
		{
			name:     "development",
			tags:     []tool.DefaultTag{tags.Connector, tags.Development},
			wantName: "Development",
			wantSort: tags.Development.SortOrder,
		},
		{
			name:     "only connector umbrella falls back to Other",
			tags:     []tool.DefaultTag{tags.Connector},
			wantName: "Other",
			wantSort: 1<<31 - 1,
		},
		{
			name:     "system connector with no category groups under System",
			tags:     []tool.DefaultTag{tags.Connector, tags.System},
			system:   true,
			wantName: tags.System.Name,
			wantSort: tags.System.SortOrder,
		},
		{
			name:     "category wins over system when both present",
			tags:     []tool.DefaultTag{tags.Connector, tags.Observability, tags.System},
			system:   true,
			wantName: "Observability",
			wantSort: tags.Observability.SortOrder,
		},
		{
			// A custom connector's per-def access tag ("custom:<key>") is NOT a
			// category — it must be skipped so the card lands in "Other", never
			// a "custom:beo_echo" chip of its own.
			name:     "custom access tag is skipped, falls back to Other",
			tags:     []tool.DefaultTag{tags.Connector, {Name: "custom:beo_echo"}},
			wantName: "Other",
			wantSort: 1<<31 - 1,
		},
		{
			name:     "real category wins over a custom access tag",
			tags:     []tool.DefaultTag{tags.Connector, {Name: "custom:beo_echo"}, tags.Communication},
			wantName: "Communication",
			wantSort: tags.Communication.SortOrder,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotSort, _ := connectorCategory(tc.tags, tc.system)
			if gotName != tc.wantName || gotSort != tc.wantSort {
				t.Fatalf("connectorCategory() = (%q, %d), want (%q, %d)", gotName, gotSort, tc.wantName, tc.wantSort)
			}
		})
	}
}
