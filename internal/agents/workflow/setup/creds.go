package setup

import (
	"context"
	"strings"

	connectorsvc "github.com/yogasw/wick/internal/connectors"
)

// ConnectorsCredsAdapter wraps the connectors service to expose a
// connector.RowCredsFn for the workflow registry. Lookup is by
// (module key, row label) — first matching label wins; empty row
// falls back to the first instance for that Key.
func ConnectorsCredsAdapter(svc *connectorsvc.Service) func(module, row string) (map[string]string, error) {
	return func(module, row string) (map[string]string, error) {
		rows, err := svc.ListByKey(context.Background(), module)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return map[string]string{}, nil
		}
		want := strings.TrimSpace(row)
		for _, r := range rows {
			if want == "" || strings.EqualFold(r.Label, want) || r.ID == want {
				return svc.LoadConfigs(r), nil
			}
		}
		// No label match — fall back to first row so YAML stays usable.
		return svc.LoadConfigs(rows[0]), nil
	}
}
