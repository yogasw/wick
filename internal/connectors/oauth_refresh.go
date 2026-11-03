package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// oauthRefreshMargin renews an access token this long before it expires, so
// an op never starts with a token that dies mid-call.
const oauthRefreshMargin = 2 * time.Minute

// oauthRefreshTimeout bounds the token endpoint call; a slow provider must
// not hold the op much longer than the op itself would take.
const oauthRefreshTimeout = 15 * time.Second

// oauthInstanceToken is an instance-level access token minted in this
// process and when it expires. The instance config has no expiry slot, so
// the expiry is only known to the process that minted it; another process
// (or a restart) refreshes once and then knows it too.
type oauthInstanceToken struct {
	token   string
	expires time.Time
}

// oauthRefreshState is the per-Service bookkeeping for ensureOAuthToken. The
// zero value is ready to use.
type oauthRefreshState struct {
	locks    sync.Map // slot key -> *sync.Mutex
	instance sync.Map // connector id -> oauthInstanceToken
}

func (st *oauthRefreshState) lock(key string) func() {
	m, _ := st.locks.LoadOrStore(key, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// ensureOAuthToken gives an op of a generic-OAuth connector (OAuthMeta with a
// TokenURL, e.g. Google) a live access token in configs["user_token"]: when
// the stored one is missing or about to expire, it is refreshed with the
// refresh token and written back — onto the account when the call runs as
// one, onto the instance config otherwise — so the next call, in any process,
// starts with it instead of refreshing again.
//
// configs must already be decrypted. Best-effort: a failed refresh is logged
// and the op runs on what was stored, so the connector's own handling (its
// refresh, or the auth error that flags a reconnect) is unchanged. Returns
// the minted token ("" when none) for the caller's masker.
func (s *Service) ensureOAuthToken(ctx context.Context, mod connector.Module, row entity.Connector, acct *entity.ConnectorAccount, configs map[string]string) string {
	if mod.OAuth == nil || mod.OAuth.TokenURL == "" {
		return ""
	}
	if configs["refresh_token"] == "" || configs["client_id"] == "" {
		return ""
	}

	slot := "instance:" + row.ID
	if acct != nil {
		slot = "account:" + acct.ID
	}
	if s.oauthTokenFresh(ctx, row, acct, configs, false) {
		return ""
	}

	unlock := s.oauth.lock(slot)
	defer unlock()
	// Another call may have refreshed while this one waited for the lock.
	if s.oauthTokenFresh(ctx, row, acct, configs, true) {
		return ""
	}

	tok, err := refreshOAuthToken(ctx, s.httpClient, mod.OAuth.TokenURL, configs)
	if err != nil {
		log.Warn().Err(err).Str("connector", row.Key).Str("connector_id", row.ID).
			Msg("connectors: oauth token refresh failed; running on the stored token")
		return ""
	}
	var expiresAt *time.Time
	if tok.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		expiresAt = &t
	}
	if err := s.saveOAuthToken(ctx, row, acct, tok, expiresAt); err != nil {
		log.Warn().Err(err).Str("connector", row.Key).Str("connector_id", row.ID).
			Msg("connectors: refreshed oauth token not saved; the next call refreshes again")
	}
	configs["user_token"] = tok.AccessToken
	return tok.AccessToken
}

// oauthTokenFresh reports whether the token the op would run with is still
// good for at least oauthRefreshMargin. reload re-reads the account so a
// refresh another call just saved is picked up; on true it also puts that
// token into configs.
func (s *Service) oauthTokenFresh(ctx context.Context, row entity.Connector, acct *entity.ConnectorAccount, configs map[string]string, reload bool) bool {
	if acct != nil {
		cur := acct
		if reload {
			fresh, err := s.repo.GetAccountByID(ctx, acct.ID)
			if err != nil || fresh == nil {
				return false
			}
			cur = fresh
		}
		if cur.AccessToken == "" || cur.ExpiresAt == nil || time.Until(*cur.ExpiresAt) < oauthRefreshMargin {
			return false
		}
		configs["user_token"] = cur.AccessToken
		return true
	}
	v, ok := s.oauth.instance.Load(row.ID)
	if !ok {
		return false
	}
	it := v.(oauthInstanceToken)
	if time.Until(it.expires) < oauthRefreshMargin {
		return false
	}
	configs["user_token"] = it.token
	return true
}

// saveOAuthToken writes a refreshed token set back where it came from.
func (s *Service) saveOAuthToken(ctx context.Context, row entity.Connector, acct *entity.ConnectorAccount, tok *oauthTokenResponse, expiresAt *time.Time) error {
	if acct != nil {
		// A refresh token outlives the access token, so it is never stored in
		// plaintext (same rule as SaveAccountTokens). "" keeps the stored one.
		refresh := tok.RefreshToken
		if refresh != "" && s.enc != nil && !s.enc.Disabled() && !enc.IsMasterToken(refresh) {
			var err error
			if refresh, err = s.enc.EncryptMaster(refresh); err != nil {
				return fmt.Errorf("encrypt refresh token: %w", err)
			}
		}
		return s.repo.UpdateAccountTokens(ctx, acct.ID, tok.AccessToken, refresh, expiresAt)
	}

	if expiresAt != nil {
		s.oauth.instance.Store(row.ID, oauthInstanceToken{token: tok.AccessToken, expires: *expiresAt})
	}
	if s.cfgs == nil {
		return nil
	}
	owner := ownerForConnector(row.ID)
	declared := map[string]bool{}
	for _, c := range s.cfgs.ListOwned(owner) {
		declared[c.Key] = true
	}
	if declared["user_token"] {
		if err := s.cfgs.SetOwned(ctx, owner, "user_token", tok.AccessToken); err != nil {
			return fmt.Errorf("save user_token: %w", err)
		}
	}
	// A rotated refresh token replaces the instance copy only where the OAuth
	// callback keeps one (single-account rows); see oauthCallback.
	if tok.RefreshToken != "" && declared["refresh_token"] && !row.MultiAccount {
		if err := s.cfgs.SetOwned(ctx, owner, "refresh_token", tok.RefreshToken); err != nil {
			return fmt.Errorf("save refresh_token: %w", err)
		}
	}
	return nil
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// refreshOAuthToken runs the RFC 6749 refresh_token grant against tokenURL
// with the instance's client credentials.
func refreshOAuthToken(ctx context.Context, hc *http.Client, tokenURL string, configs map[string]string) (*oauthTokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthRefreshTimeout)
	defer cancel()

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", configs["refresh_token"])
	data.Set("client_id", configs["client_id"])
	data.Set("client_secret", configs["client_secret"])

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}

	var out oauthTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("refresh endpoint %d: undecodable response", resp.StatusCode)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("refresh error: %s: %s", out.Error, out.ErrorDesc)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh endpoint %d", resp.StatusCode)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in refresh response")
	}
	return &out, nil
}
