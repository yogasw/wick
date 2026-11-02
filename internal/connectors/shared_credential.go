package connectors

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// sharedCredentialKeys are the instance-level configs an OAuth row runs on
// when a call resolved no connected account. On a per-user (SSO) row they are
// a legacy shared credential: whoever filled them, everyone without an
// account of their own acts as them.
var sharedCredentialKeys = []string{"user_token", "refresh_token"}

// SharedCredentialDefault is the answer for an instance that never saved
// one. A multi-account row is per-user by design, so it starts OFF: nobody
// silently borrows another person's identity. A single-account row has one
// identity the whole instance shares, so it starts ON and keeps working.
func SharedCredentialDefault(row entity.Connector) bool {
	return !row.MultiAccount
}

// sharedCredentialApplies reports whether the policy means anything for
// this row: only an OAuth connector with SSO on has per-user accounts to
// fall back FROM. Anywhere else the instance credential is the only one.
func sharedCredentialApplies(mod connector.Module, row entity.Connector) bool {
	return mod.OAuth != nil && row.EnableSSO
}

// SharedCredentialAllowed reports whether a call that resolved no connected
// account may run on the instance-level user_token / refresh_token.
func (s *Service) SharedCredentialAllowed(ctx context.Context, row entity.Connector) (bool, error) {
	mod, ok := s.Module(row.Key)
	if !ok || !sharedCredentialApplies(mod, row) {
		return true, nil
	}
	stored, err := s.repo.SharedCredentialPolicy(ctx, row.ID)
	if err != nil {
		return false, fmt.Errorf("load shared credential policy: %w", err)
	}
	if stored == nil {
		return SharedCredentialDefault(row), nil
	}
	return *stored, nil
}

// SetSharedCredentialAllowed saves the per-instance policy. Turning it off
// also clears the stored instance user_token / refresh_token: a credential
// nothing may run on is only a liability left in the database. Calling it
// with false again is how the manager clears a legacy value on a row that
// was already off by default.
func (s *Service) SetSharedCredentialAllowed(ctx context.Context, row entity.Connector, allowed bool) error {
	if err := s.repo.SetSharedCredentialPolicy(ctx, row.ID, allowed); err != nil {
		return err
	}
	if allowed || s.cfgs == nil {
		return nil
	}
	stored := s.LoadConfigs(row)
	for _, k := range sharedCredentialKeys {
		if stored[k] == "" {
			continue
		}
		if err := s.cfgs.ClearOwned(ctx, ownerForConnector(row.ID), k); err != nil {
			return fmt.Errorf("clear %s: %w", k, err)
		}
	}
	return nil
}

// SharedCredentialStored reports whether the instance still holds a value in
// any of the shared credential slots, so the manager can offer to clear it.
func (s *Service) SharedCredentialStored(row entity.Connector) bool {
	stored := s.LoadConfigs(row)
	for _, k := range sharedCredentialKeys {
		if stored[k] != "" {
			return true
		}
	}
	return false
}

// OwnAccount returns the account userID connected on this instance, or nil.
// Several (a multi-account row lets one person connect more than one
// identity) resolve deterministically: an account without an auth error
// first, then the most recently updated, so a fresh reconnect wins.
func (s *Service) OwnAccount(ctx context.Context, connectorID, userID string) (*entity.ConnectorAccount, error) {
	if userID == "" {
		return nil, nil
	}
	accs, err := s.repo.ListAccounts(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	var mine []entity.ConnectorAccount
	for _, a := range accs {
		if a.WickUserID == userID {
			mine = append(mine, a)
		}
	}
	if len(mine) == 0 {
		return nil, nil
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if (mine[i].AuthError == "") != (mine[j].AuthError == "") {
			return mine[i].AuthError == ""
		}
		return mine[i].UpdatedAt.After(mine[j].UpdatedAt)
	})
	return &mine[0], nil
}

// ConnectURL is where a person connects their own account on this instance:
// the manager detail page, absolute when the app URL is configured.
func (s *Service) ConnectURL(row entity.Connector) string {
	path := "/manager/connectors/" + row.Key + "/" + row.ID
	if s.cfgs == nil {
		return path
	}
	return strings.TrimRight(s.cfgs.AppURL(), "/") + path
}

// connectNeededError is what a call gets when it has no account of its own
// and the instance credential is off. Agents relay it verbatim, so it says
// what is missing and where to fix it — and never carries a secret.
func (s *Service) connectNeededError(mod connector.Module, row entity.Connector) error {
	name := mod.Meta.Name
	if mod.OAuth != nil && mod.OAuth.DisplayName != "" {
		name = mod.OAuth.DisplayName
	}
	return fmt.Errorf("no connected %s account for you on %q: connect your own account at %s", name, row.Label, s.ConnectURL(row))
}
