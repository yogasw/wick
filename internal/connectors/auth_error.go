package connectors

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// authErrorMarkers are error texts that mean the account's credential is
// no longer accepted: the provider refused the token or its refresh, so
// only a fresh consent (reconnect) helps. Lower-cased. A plain 403 is NOT
// one of them: that is usually a file or channel the account may not
// open, which a reconnect does not fix.
var authErrorMarkers = []string{
	"invalid_grant",
	"invalid_token",
	"token_revoked",
	"token_expired",
	"invalid_auth",
	"not_authed",
	"account_inactive",
	"unauthenticated",
	"unregistered callers",
	"unauthorized after token refresh",
	"access token expired",
	"refresh error:",
	" 401:",
	"status 401",
	"401 unauthorized",
}

// IsAuthError reports whether msg is an op error a reconnect would fix.
func IsAuthError(msg string) bool {
	m := strings.ToLower(msg)
	for _, k := range authErrorMarkers {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// authErrorMaxLen bounds the stored message; it is a hint, not a log.
const authErrorMaxLen = 300

// trackAccountAuth flags acct as needing a reconnect when the op failed on
// auth, and clears a flag a later successful call proves stale.
// Best-effort: a failed write never fails the op.
func (s *Service) trackAccountAuth(ctx context.Context, acct *entity.ConnectorAccount, res *ExecuteResult) {
	if acct == nil || res == nil {
		return
	}
	var msg string
	switch {
	case res.Status == entity.ConnectorRunStatusError && IsAuthError(res.ErrorMessage):
		msg = strings.TrimSpace(res.ErrorMessage)
		if r := []rune(msg); len(r) > authErrorMaxLen {
			msg = string(r[:authErrorMaxLen]) + "…"
		}
	case res.Status == entity.ConnectorRunStatusSuccess && acct.AuthError != "":
	default:
		return
	}
	if err := s.repo.SetAccountAuthError(ctx, acct.ID, msg); err != nil {
		log.Warn().Err(err).Str("account_id", acct.ID).Msg("connectors: account auth flag not saved")
		return
	}
	if msg != "" {
		log.Info().Str("account_id", acct.ID).Str("account", acct.DisplayName).Msg("connectors: account needs reconnect")
	}
}
