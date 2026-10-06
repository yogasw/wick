package connectors

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/enc"
)

// ErrAccountMismatch is returned when an account does not belong to the
// connector, or to the wick user it is asked for.
var ErrAccountMismatch = errors.New("connector account does not belong to this user")

// ResolveToken returns the plaintext token a connector row posts with, for
// in-process callers that talk to the service themselves (a remote agent
// adapter). accountID empty = the instance's own token (auth_mode); set =
// that OAuth account's token, which must belong to connectorID and to
// userID. The token must never be logged or sent back to a client.
func (s *Service) ResolveToken(ctx context.Context, connectorID, accountID, userID string) (string, error) {
	row, err := s.Get(ctx, connectorID)
	if err != nil {
		return "", err
	}
	if row.Disabled {
		return "", fmt.Errorf("connector %s is disabled", row.Label)
	}
	cfg := s.LoadConfigs(*row)
	key := "bot_token"
	if accountID != "" {
		acct, err := s.GetAccount(ctx, accountID)
		if err != nil {
			return "", err
		}
		if acct.ConnectorID != connectorID || acct.WickUserID == "" || acct.WickUserID != userID {
			return "", ErrAccountMismatch
		}
		cfg["user_token"], key = acct.AccessToken, "user_token"
	} else if strings.TrimSpace(cfg["auth_mode"]) == "user_token" {
		key = "user_token"
	}
	tok := strings.TrimSpace(cfg[key])
	if tok == "" && accountID == "" {
		tok = strings.TrimSpace(cfg["token"])
	}
	if tok == "" {
		return "", fmt.Errorf("connector %s has no %s", row.Label, key)
	}
	if s.enc != nil && !s.enc.Disabled() {
		if enc.IsMasterToken(tok) {
			if tok, err = s.enc.DecryptMaster(tok); err != nil {
				return "", err
			}
		}
		out, _, err := unmaskMap(s.enc, map[string]string{"t": tok}, userID)
		if err != nil {
			return "", err
		}
		tok = out["t"]
	}
	return tok, nil
}
