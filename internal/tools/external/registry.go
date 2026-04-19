package external

import (
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/tool"
)

// All returns the external-link modules. Every entry becomes a card on
// the home grid — Key must be unique across all modules (wick mounts
// it at /tools/{Key}). Tag groupings live in internal/tags/defaults.go.
func All() []tool.Module {
	return []tool.Module{
		{
			Meta: tool.Tool{
				Key:               "json-to-curl",
				Name:              "JSON to CURL",
				Description:       "Build requests, generate CURL commands, and simulate API calls.",
				Icon:              "🚀",
				ExternalURL:       "https://yogasw.my.id/utilities/json-to-curl",
				DefaultVisibility: entity.VisibilityPublic,
				DefaultTags:       []tool.DefaultTag{tags.API},
			},
			Register: Register,
		},
	}
}
