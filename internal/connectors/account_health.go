package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/pkg/connector"
)

// RunAccountHealthCheck checks only the named account. The manager authorizes
// the caller before invoking this service, just as for RunHealthCheck.
func (s *Service) RunAccountHealthCheck(ctx context.Context, connectorID, accountID, callerUserID string) (*HealthCheckResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	row, err := s.repo.Get(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	mod, ok := s.Module(row.Key)
	if !ok || mod.HealthCheck == nil {
		return nil, ErrNoHealthCheck
	}
	acc, err := s.repo.GetAccountByID(ctx, accountID)
	if err != nil || acc.ConnectorID != connectorID {
		return nil, fmt.Errorf("account not found on connector")
	}
	cfg := s.LoadConfigs(*row)
	cfg["auth_mode"] = "user_token"
	cfg["user_token"] = acc.AccessToken
	// Never refresh this account using an instance-level refresh token.
	cfg["refresh_token"] = acc.RefreshToken
	if s.enc != nil && !s.enc.Disabled() {
		for k, v := range cfg {
			if enc.IsMasterToken(v) {
				plain, err := s.enc.DecryptMaster(v)
				if err != nil {
					return nil, fmt.Errorf("decrypt account config: %w", err)
				}
				cfg[k] = plain
			}
		}
		cfg, _, err = unmaskMap(s.enc, cfg, callerUserID)
		if err != nil {
			return nil, fmt.Errorf("decrypt account config: %w", err)
		}
	}
	// Generic OAuth refresh persists fresh access tokens on the selected account.
	s.ensureOAuthToken(ctx, mod, *row, acc, cfg)
	checkedToken := cfg["user_token"]
	if checkedToken == "" {
		return nil, fmt.Errorf("account has no access token; reconnect")
	}
	report, err := mod.HealthCheck(connector.NewCtx(ctx, row.ID, cfg, nil, s.httpClient, nil, nil))
	if err != nil {
		return nil, fmt.Errorf("health check: %w", err)
	}
	locks := accountHealthLocks(acc)
	result := &HealthCheckResult{Ops: report}
	for _, h := range report {
		_, was := locks[h.Key]
		if h.OK {
			delete(locks, h.Key)
			if was {
				result.NewlyCleared = append(result.NewlyCleared, h.Key)
			}
		} else {
			locks[h.Key] = h.Reason
			if !was {
				result.NewlyLocked = append(result.NewlyLocked, h.Key)
			}
		}
	}
	raw, err := json.Marshal(locks)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetAccountHealthDisabledOps(ctx, accountID, checkedToken, string(raw)); err != nil {
		return nil, err
	}
	return result, nil
}
