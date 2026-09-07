package logintty

import (
	"errors"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// ErrUsageUnsupported marks provider types that expose no usage/limit
// API wick can read yet. codex/gemini support lands in their own files.
var ErrUsageUnsupported = errors.New("usage not supported for this provider type")

// UsageWindow is one rate-limit window's utilization (e.g. claude's
// rolling 5-hour and 7-day windows).
type UsageWindow struct {
	Key         string    `json:"key"`
	Utilization float64   `json:"utilization"` // percent, 0-100
	ResetsAt    time.Time `json:"resets_at,omitempty"`
}

// ReadUsage fetches the current rate-limit utilization for one
// instance using its stored credentials. Only claude today.
func ReadUsage(t provider.Type, env []string) ([]UsageWindow, error) {
	switch t {
	case provider.TypeClaude:
		return readClaudeUsage(env)
	default:
		return nil, ErrUsageUnsupported
	}
}
