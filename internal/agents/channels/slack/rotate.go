// Package slack — app-level token rotation.
//
// Once "Token rotation" is switched on for a Slack app (one-way), its new
// app-level tokens are xoxe.xapp-… access tokens that expire after 12h,
// issued with a single-use xoxe-1-… refresh token. The channel trades the
// refresh token for a new pair through oauth.v2.access about an hour before
// expiry, hands the pair to the owner's persist callback BEFORE using it,
// then reconnects Socket Mode with it. Legacy xapp- tokens never expire and
// take none of these paths.
//
// Token values are never logged: only their prefix and the expiry.

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
)

// IsAppToken reports whether t looks like a Slack app-level token, legacy
// (xapp-) or rotating (xoxe.xapp-).
func IsAppToken(t string) bool {
	return strings.HasPrefix(t, "xapp-") || IsRotatingAppToken(t)
}

// IsRotatingAppToken reports whether t is an app-level token issued with
// token rotation on (xoxe.xapp-), which expires and must be refreshed.
func IsRotatingAppToken(t string) bool { return strings.HasPrefix(t, "xoxe.xapp-") }

// IsBotToken reports whether t looks like a Slack bot token, legacy (xoxb-)
// or rotating (xoxe.xoxb-). Bot-token refresh is not done by wick.
func IsBotToken(t string) bool {
	return strings.HasPrefix(t, "xoxb-") || strings.HasPrefix(t, "xoxe.xoxb-")
}

// tokenPrefix is what may be logged about a token: everything up to the
// first digit run ("xoxe.xapp-1-"), never the secret part.
func tokenPrefix(t string) string {
	for _, p := range []string{"xoxe.xapp-", "xoxe.xoxb-", "xoxe-", "xapp-", "xoxb-"} {
		if strings.HasPrefix(t, p) {
			return p
		}
	}
	return ""
}

// PersistAppTokenFn stores a freshly rotated app token pair and its expiry
// wherever the channel's config lives. It runs before the new token is used:
// the old refresh token is spent, so a pair that is not stored is lost on
// the next restart.
type PersistAppTokenFn func(accessToken, refreshToken string, expiresAt time.Time) error

// AppTokenRotation is the rotation state reported by health and status.
type AppTokenRotation struct {
	Rotating    bool
	ExpiresAt   time.Time // zero = not known yet
	LastRefresh time.Time // zero = not refreshed since boot
	LastError   string    // "" = last refresh (and persist) succeeded
}

// LoadAppTokenFn reads the app token pair currently stored for the channel,
// so a refresh can first adopt a pair another instance of the same app
// already rotated instead of spending the same refresh token again.
type LoadAppTokenFn func() (accessToken, refreshToken string, expiresAt time.Time, err error)

// rotationState is the mutable part of AppTokenRotation. Guarded by cfgMu.
type rotationState struct {
	expiresAt   time.Time
	lastRefresh time.Time
	lastError   string
	// persistFailed marks lastError as a failed save, cleared by the next
	// successful one.
	persistFailed bool
}

// rotatedPair is an app token pair still to be written to the store.
type rotatedPair struct {
	access, refresh string
	expiresAt       time.Time
}

var (
	// slackOAuthURL is the Web API base oauth.v2.access is called on.
	// A var for tests; a Channel's own oauthURL wins when set.
	slackOAuthURL = "https://slack.com/api/"
	// rotateLead is how long before expiry the token is refreshed, and
	// rotateMinDelay the shortest wait the scheduler ever takes.
	rotateLead     = time.Hour
	rotateMinDelay = time.Minute
	// rotateRetryDelay is the first wait after a failed scheduled refresh,
	// doubled per failure up to rotateRetryMax.
	rotateRetryDelay = 5 * time.Minute
	rotateRetryMax   = time.Hour
	// persistRetryMax caps the wait between retries of a failed save.
	persistRetryMax = 5 * time.Minute
	// rotateExchangeTimeout bounds one refresh + save. It runs detached
	// from the channel's run, so a Reload or Stop never aborts it halfway.
	rotateExchangeTimeout = 30 * time.Second
	// defaultAppTokenTTL is assumed when Slack answers without expires_in.
	defaultAppTokenTTL = 12 * time.Hour
	// rotateHTTP is the client oauth.v2.access goes through.
	rotateHTTP = &http.Client{Timeout: 30 * time.Second}
)

// appRefreshLocks serialises refreshes per Slack app across every channel
// instance in the process (a re-created instance, the same app on two
// rows), keyed by client ID.
var appRefreshLocks sync.Map

func appRefreshLock(clientID string) *sync.Mutex {
	mu, _ := appRefreshLocks.LoadOrStore(clientID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// permanentOAuthErr reports an oauth.v2.access error retrying cannot cure.
func permanentOAuthErr(err error) bool {
	switch err.Error() {
	case "invalid_refresh_token", "invalid_client_id", "bad_client_secret", "invalid_grant_type", "invalid_client":
		return true
	}
	return false
}

// tokenRefresh is a parsed oauth.v2.access answer.
type tokenRefresh struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// exchangeRefreshToken trades refreshToken for a new access + refresh pair.
func exchangeRefreshToken(ctx context.Context, baseURL, clientID, clientSecret, refreshToken string) (tokenRefresh, error) {
	if connectOnlyMode() {
		return tokenRefresh{}, errors.New("Slack connection-only mode blocks token refresh")
	}
	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/oauth.v2.access", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenRefresh{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := rotateHTTP.Do(req)
	if err != nil {
		return tokenRefresh{}, err
	}
	defer resp.Body.Close()
	var body struct {
		OK           bool   `json:"ok"`
		Error        string `json:"error"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return tokenRefresh{}, fmt.Errorf("oauth.v2.access: HTTP %d: %w", resp.StatusCode, err)
	}
	if !body.OK {
		if body.Error == "" {
			body.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return tokenRefresh{}, errors.New(body.Error)
	}
	if body.AccessToken == "" || body.RefreshToken == "" {
		return tokenRefresh{}, errors.New("oauth.v2.access returned no access_token or refresh_token")
	}
	return tokenRefresh{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken, ExpiresIn: body.ExpiresIn}, nil
}

// SetPersistAppTokenFn registers the callback that stores a rotated app
// token pair. Wired by whoever owns the channel's config row; nil = the
// pair lives in memory only (lost on restart).
func (s *Channel) SetPersistAppTokenFn(fn PersistAppTokenFn) {
	s.cfgMu.Lock()
	s.persistAppToken = fn
	s.cfgMu.Unlock()
}

// SetLoadAppTokenFn registers the reader of the stored app token pair (see
// LoadAppTokenFn). nil = no re-read before a refresh.
func (s *Channel) SetLoadAppTokenFn(fn LoadAppTokenFn) {
	s.cfgMu.Lock()
	s.loadAppToken = fn
	s.cfgMu.Unlock()
}

// AppTokenRotation reports the rotation state of the app token in use.
func (s *Channel) AppTokenRotation() AppTokenRotation {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return AppTokenRotation{
		Rotating:    IsRotatingAppToken(s.cfg.AppToken),
		ExpiresAt:   s.rot.expiresAt,
		LastRefresh: s.rot.lastRefresh,
		LastError:   s.rot.lastError,
	}
}

// rotationMissing names the settings a rotating app token still needs, ""
// when complete or when the token does not rotate.
func rotationMissing(appToken, refreshToken, clientID, clientSecret string) string {
	if !IsRotatingAppToken(appToken) {
		return ""
	}
	var miss []string
	if refreshToken == "" {
		miss = append(miss, "refresh token")
	}
	if clientID == "" {
		miss = append(miss, "client ID")
	}
	if clientSecret == "" {
		miss = append(miss, "client secret")
	}
	return strings.Join(miss, ", ")
}

// errRotationIncomplete is returned when a rotating token cannot be
// refreshed for lack of credentials.
var errRotationIncomplete = errors.New("rotating app token needs a refresh token, client ID and client secret")

// refreshAppToken refreshes the rotating app token and reports whether the
// token in use changed. Single-flight: callers pass the token they saw, and
// one that finds it already replaced by the time it holds rotMu returns at
// once; refreshes of the same app across instances queue on its client ID,
// and a pair another instance already stored is adopted instead of spending
// the same refresh token twice.
//
// The exchange and the save run on a context detached from ctx with their
// own timeout: once Slack has rotated the pair it is the only one that
// works, so a Reload or Stop landing meanwhile must not throw it away.
func (s *Channel) refreshAppToken(ctx context.Context, seen string) (bool, error) {
	s.rotMu.Lock()
	defer s.rotMu.Unlock()

	s.cfgMu.Lock()
	cfg := s.cfg
	persist := s.persistAppToken
	load := s.loadAppToken
	base := s.oauthURL
	s.cfgMu.Unlock()
	if cfg.AppToken != seen || !IsRotatingAppToken(cfg.AppToken) {
		return false, nil
	}
	if rotationMissing(cfg.AppToken, cfg.AppRefreshToken, cfg.ClientID, cfg.ClientSecret) != "" {
		s.setRotationError("Refresh failed: " + errRotationIncomplete.Error() + ".")
		return false, errRotationIncomplete
	}
	if base == "" {
		base = slackOAuthURL
	}

	lk := appRefreshLock(cfg.ClientID)
	lk.Lock()
	defer lk.Unlock()

	xctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rotateExchangeTimeout)
	defer cancel()

	if load != nil {
		if access, refresh, exp, err := load(); err == nil && s.adoptStored(access, refresh, exp) {
			log.Info().Str("channel", "slack").Str("bot", s.logBot()).Str("token", tokenPrefix(access)).
				Time("expires_at", exp).Msg("app token already refreshed elsewhere, adopted the stored pair")
			return true, nil
		}
	}

	got, err := exchangeRefreshToken(xctx, base, cfg.ClientID, cfg.ClientSecret, cfg.AppRefreshToken)
	if err != nil {
		msg := "Refresh failed: " + err.Error() + ". Retrying automatically."
		if permanentOAuthErr(err) {
			msg = "Refresh failed: " + err.Error() + ". Generate a new app-level token + refresh token and paste both again."
		}
		s.setRotationError(msg)
		log.Error().Str("channel", "slack").Str("bot", s.logBot()).Err(err).Msg("app token refresh failed")
		return false, err
	}
	ttl := time.Duration(got.ExpiresIn) * time.Second
	if got.ExpiresIn <= 0 {
		ttl = defaultAppTokenTTL
		log.Warn().Str("channel", "slack").Str("bot", s.logBot()).Dur("assumed_ttl", ttl).
			Msg("oauth.v2.access answered without expires_in")
	}
	now := time.Now()
	pair := rotatedPair{access: got.AccessToken, refresh: got.RefreshToken, expiresAt: now.Add(ttl)}

	// Stored first: the old refresh token is spent now, so this pair is
	// the only one that still works after a restart. A failed save is
	// retried by the scheduler (see flushPendingPersist).
	var perr error
	if persist != nil {
		perr = persist(pair.access, pair.refresh, pair.expiresAt)
	}

	s.cfgMu.Lock()
	s.noteRotatedLocked(cfg.AppToken, pair.access)
	s.noteRotatedLocked(cfg.AppRefreshToken, pair.refresh)
	s.setPairLocked(pair)
	s.rot = rotationState{expiresAt: pair.expiresAt, lastRefresh: now}
	s.pending = nil
	if perr != nil {
		s.pending = &pair
		s.rot.lastError = persistErrMsg(perr)
		s.rot.persistFailed = true
	}
	s.cfgMu.Unlock()

	if perr != nil {
		log.Error().Str("channel", "slack").Str("bot", s.logBot()).Err(perr).Msg("app token refreshed but not saved, will retry")
	}
	log.Info().Str("channel", "slack").Str("bot", s.logBot()).Str("token", tokenPrefix(pair.access)).
		Time("expires_at", pair.expiresAt).Msg("app token refreshed")
	return true, nil
}

func persistErrMsg(err error) string {
	return "Saving the refreshed token failed: " + err.Error() + ". Retrying — it works until wick restarts."
}

// setPairLocked makes p the app token pair in use. Caller holds cfgMu.
func (s *Channel) setPairLocked(p rotatedPair) {
	s.cfg.AppToken = p.access
	s.cfg.AppRefreshToken = p.refresh
	s.cfg.AppTokenExpiresAt = ""
	if !p.expiresAt.IsZero() {
		s.cfg.AppTokenExpiresAt = p.expiresAt.UTC().Format(time.RFC3339)
	}
	s.api = slackgo.New(s.cfg.BotToken, slackgo.OptionAppLevelToken(p.access), slackgo.OptionHTTPClient(restrictedSlackHTTPClient()))
}

// adoptStored switches to the stored pair when it is newer than the one in
// use: a rotating pair with a different refresh token that is not one this
// instance already rotated away from.
func (s *Channel) adoptStored(access, refresh string, expiresAt time.Time) bool {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	cur := s.cfg.AppRefreshToken
	if !IsRotatingAppToken(access) || refresh == "" || refresh == cur {
		return false
	}
	// With a save still pending the store is behind this instance: its
	// pair is an old one of ours (possibly evicted from rotAliases), never
	// one to adopt.
	if s.pending != nil {
		return false
	}
	if _, own := s.rotAliases[refresh]; own || refresh == s.tokenOriginLocked(cur) {
		return false
	}
	s.setPairLocked(rotatedPair{access: access, refresh: refresh, expiresAt: expiresAt})
	s.rot = rotationState{expiresAt: expiresAt, lastRefresh: s.rot.lastRefresh}
	s.pending = nil
	return true
}

// flushPendingPersist retries saving a pair the store does not hold yet.
// Reports whether nothing is left pending.
func (s *Channel) flushPendingPersist() bool {
	s.rotMu.Lock()
	defer s.rotMu.Unlock()
	s.cfgMu.Lock()
	p := s.pending
	persist := s.persistAppToken
	s.cfgMu.Unlock()
	if p == nil {
		return true
	}
	if persist == nil {
		s.cfgMu.Lock()
		s.pending = nil
		s.cfgMu.Unlock()
		return true
	}
	if err := persist(p.access, p.refresh, p.expiresAt); err != nil {
		s.cfgMu.Lock()
		s.rot.lastError = persistErrMsg(err)
		s.rot.persistFailed = true
		s.cfgMu.Unlock()
		log.Error().Str("channel", "slack").Str("bot", s.logBot()).Str("token", tokenPrefix(p.access)).
			Err(err).Msg("saving the rotated app token failed again")
		return false
	}
	s.cfgMu.Lock()
	if s.pending == p {
		s.pending = nil
	}
	if s.rot.persistFailed {
		s.rot.lastError, s.rot.persistFailed = "", false
	}
	s.cfgMu.Unlock()
	log.Info().Str("channel", "slack").Str("bot", s.logBot()).Str("token", tokenPrefix(p.access)).
		Msg("rotated app token saved")
	return true
}

func (s *Channel) setRotationError(msg string) {
	s.cfgMu.Lock()
	s.rot.lastError = msg
	s.rot.persistFailed = false
	s.cfgMu.Unlock()
}

// maxRotatedAliases bounds the remembered rotated tokens per channel.
const maxRotatedAliases = 16

// noteRotatedLocked remembers that next replaced prev by rotation, so the
// stored row now holding next still fingerprints (and reloads) as the token
// the operator configured. Caller holds cfgMu.
func (s *Channel) noteRotatedLocked(prev, next string) {
	if prev == "" || next == "" {
		return
	}
	if s.rotAliases == nil {
		s.rotAliases = map[string]string{}
	}
	s.rotAliases[next] = s.tokenOriginLocked(prev)
	s.rotAliasOrder = append(s.rotAliasOrder, next)
	for len(s.rotAliasOrder) > maxRotatedAliases {
		delete(s.rotAliases, s.rotAliasOrder[0])
		s.rotAliasOrder = s.rotAliasOrder[1:]
	}
}

// tokenOriginLocked maps a token wick rotated itself back to the one the
// operator configured; any other token maps to itself. Caller holds cfgMu.
func (s *Channel) tokenOriginLocked(t string) string {
	if o, ok := s.rotAliases[t]; ok {
		return o
	}
	return t
}

// tokenOrigin is tokenOriginLocked for ConfigSource.Hash.
func (s *Channel) tokenOrigin(t string) string {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.tokenOriginLocked(t)
}

// nextRefreshDelay is how long the scheduler waits before refreshing a
// token expiring at expiresAt: rotateLead before expiry, never less than
// rotateMinDelay. Unknown expiry refreshes now.
func nextRefreshDelay(expiresAt, now time.Time) time.Duration {
	if expiresAt.IsZero() {
		return 0
	}
	return max(expiresAt.Sub(now)-rotateLead, rotateMinDelay)
}

// refreshRetryDelay is the wait after the n-th consecutive failed refresh.
func refreshRetryDelay(n int) time.Duration {
	d := rotateRetryDelay
	for i := 1; i < n && d < rotateRetryMax; i++ {
		d *= 2
	}
	return min(d, rotateRetryMax)
}

// rotateLoop keeps a rotating app token fresh for the life of runCtx and
// retries a save that failed. After a refresh that changed the token the
// socket is cycled so slack-go's own reconnects open with the new token.
// startFailed says serve's refresh at start already failed once.
func (s *Channel) rotateLoop(runCtx context.Context, startFailed bool) {
	failures := 0
	var retryAt time.Time
	if startFailed {
		failures = 1
		retryAt = time.Now().Add(refreshRetryDelay(1))
	}
	persistWait := time.Duration(0)
	for {
		s.cfgMu.Lock()
		token := s.cfg.AppToken
		expiresAt := s.rot.expiresAt
		pending := s.pending != nil
		s.cfgMu.Unlock()
		if !IsRotatingAppToken(token) {
			return
		}
		delay := nextRefreshDelay(expiresAt, time.Now())
		if !retryAt.IsZero() {
			delay = max(time.Until(retryAt), 0)
		}
		persistOnly := pending && persistWait < delay
		if persistOnly {
			delay = persistWait
		}
		t := time.NewTimer(delay)
		select {
		case <-runCtx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if pending {
			if s.flushPendingPersist() {
				persistWait = 0
			} else {
				persistWait = min(max(persistWait*2, rotateMinDelay), persistRetryMax)
			}
		}
		if persistOnly {
			continue
		}
		rotated, err := s.refreshAppToken(runCtx, token)
		if err != nil {
			if runCtx.Err() != nil {
				return
			}
			failures++
			retryAt = time.Now().Add(refreshRetryDelay(failures))
			continue
		}
		failures, retryAt = 0, time.Time{}
		if rotated {
			s.cycleSocket()
		}
	}
}

// cycleSocket ends the current socket run so serve opens a new one, right
// away and with the current client.
func (s *Channel) cycleSocket() {
	s.cfgMu.Lock()
	cancel := s.socketCancel
	s.cfgMu.Unlock()
	if cancel != nil {
		s.socketCycled.Store(true)
		cancel()
	}
}

// isAuthErr reports a socket error a token refresh may cure.
func isAuthErr(err error) bool {
	if err == nil {
		return false
	}
	switch err.Error() {
	case "invalid_auth", "token_expired", "not_authed", "token_revoked":
		return true
	}
	return false
}
