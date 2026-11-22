package connectors

import (
	"context"

	"github.com/yogasw/wick/pkg/connector"
)

// TrackFor resolves how a successful call of one op should be followed:
// the instance's op pref (op_pref_set track=…) wins, else the op's code
// default. Returns the op too (its name labels the checklist item). nil
// spec = the op starts nothing to follow; an unreadable pref is treated as
// none rather than failing the call that already succeeded.
func (s *Service) TrackFor(ctx context.Context, connectorID, opKey string) (*connector.Track, connector.Operation) {
	row, err := s.Get(ctx, connectorID)
	if err != nil || row == nil {
		return nil, connector.Operation{}
	}
	mod, ok := s.Module(row.Key)
	if !ok {
		return nil, connector.Operation{}
	}
	var op connector.Operation
	found := false
	for _, o := range mod.AllOps() {
		if o.Key == opKey {
			op, found = o, true
			break
		}
	}
	if !found {
		return nil, connector.Operation{}
	}
	if p, ok := s.OpPrefs(ctx, connectorID)[connectorID+"/"+opKey]; ok && p.Track != "" {
		if t, err := connector.ParseTrack(p.Track); err == nil && t != nil {
			return t, op
		}
	}
	if op.Track != nil && op.Track.Validate() == nil {
		t := *op.Track
		return &t, op
	}
	return nil, op
}
