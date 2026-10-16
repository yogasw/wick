package slack

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slack-go/slack/socketmode"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// oauthStub answers oauth.v2.access with a numbered pair per call (or the
// given failure) and counts the calls.
type oauthStub struct {
	srv   *httptest.Server
	calls atomic.Int32
	delay time.Duration
	fail  string // non-empty: answer ok:false with this error
	noTTL bool   // answer without expires_in
	form  chan map[string]string
}

func newOAuthStub(t *testing.T) *oauthStub {
	t.Helper()
	o := &oauthStub{form: make(chan map[string]string, 16)}
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := o.calls.Add(1)
		if r.URL.Path != "/oauth.v2.access" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		select {
		case o.form <- map[string]string{
			"client_id": r.PostForm.Get("client_id"), "client_secret": r.PostForm.Get("client_secret"),
			"grant_type": r.PostForm.Get("grant_type"), "refresh_token": r.PostForm.Get("refresh_token"),
		}:
		default:
		}
		time.Sleep(o.delay)
		w.Header().Set("Content-Type", "application/json")
		if o.fail != "" {
			fmt.Fprintf(w, `{"ok":false,"error":%q}`, o.fail)
			return
		}
		if o.noTTL {
			fmt.Fprintf(w, `{"ok":true,"access_token":"xoxe.xapp-1-NEW%d","refresh_token":"xoxe-1-REF%d"}`, n, n)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"access_token":"xoxe.xapp-1-NEW%d","token_type":"app_level","expires_in":43200,"refresh_token":"xoxe-1-REF%d"}`, n, n)
	}))
	t.Cleanup(o.srv.Close)
	return o
}

func rotatingChannel(o *oauthStub, expiresAt string) *Channel {
	cfg := agentconfig.SlackChannelConfig{
		BotToken: "xoxb-test", AppToken: "xoxe.xapp-1-OLD", AppRefreshToken: "xoxe-1-OLDREF",
		ClientID: o.srv.URL, ClientSecret: "shh", AppTokenExpiresAt: expiresAt,
	}
	s := NewWithOwnerCached(cfg, "", "UBOT", "Bot", "Team")
	s.oauthURL = o.srv.URL + "/"
	return s
}

func TestTokenPrefixHelpers(t *testing.T) {
	for tok, want := range map[string][3]bool{ // app, rotating app, bot
		"xapp-1-A":        {true, false, false},
		"xoxe.xapp-1-A":   {true, true, false},
		"xoxb-1-B":        {false, false, true},
		"xoxe.xoxb-1-B":   {false, false, true},
		"xoxe-1-REFRESH":  {false, false, false},
		"":                {false, false, false},
		"xoxp-1-USER":     {false, false, false},
		"Xapp-wrong-case": {false, false, false},
	} {
		got := [3]bool{IsAppToken(tok), IsRotatingAppToken(tok), IsBotToken(tok)}
		if got != want {
			t.Errorf("%q: got %v want %v", tok, got, want)
		}
	}
	if p := tokenPrefix("xoxe.xapp-1-SECRET"); p != "xoxe.xapp-" {
		t.Errorf("tokenPrefix = %q", p)
	}
}

func TestExchangeRefreshTokenSuccess(t *testing.T) {
	o := newOAuthStub(t)
	got, err := exchangeRefreshToken(context.Background(), o.srv.URL, "cid", "csec", "xoxe-1-R")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "xoxe.xapp-1-NEW1" || got.RefreshToken != "xoxe-1-REF1" || got.ExpiresIn != 43200 {
		t.Fatalf("got %+v", got)
	}
	f := <-o.form
	want := map[string]string{"client_id": "cid", "client_secret": "csec", "grant_type": "refresh_token", "refresh_token": "xoxe-1-R"}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("form %s = %q, want %q", k, f[k], v)
		}
	}
}

func TestExchangeRefreshTokenSlackError(t *testing.T) {
	o := newOAuthStub(t)
	o.fail = "invalid_refresh_token"
	_, err := exchangeRefreshToken(context.Background(), o.srv.URL, "cid", "csec", "xoxe-1-R")
	if err == nil || err.Error() != "invalid_refresh_token" {
		t.Fatalf("err = %v", err)
	}
}

func TestExchangeRefreshTokenMissingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"expires_in":43200}`))
	}))
	defer srv.Close()
	_, err := exchangeRefreshToken(context.Background(), srv.URL, "cid", "csec", "xoxe-1-R")
	if err == nil || !strings.Contains(err.Error(), "no access_token") {
		t.Fatalf("err = %v", err)
	}
}

// The pair is persisted while the old token is still the one in use, then
// swapped in; the row now holding it still fingerprints as the original.
func TestRefreshPersistsBeforeUse(t *testing.T) {
	o := newOAuthStub(t)
	s := rotatingChannel(o, "")
	var persisted [2]string
	var expiry time.Time
	s.SetPersistAppTokenFn(func(a, r string, exp time.Time) error {
		s.cfgMu.Lock()
		inUse := s.cfg.AppToken
		s.cfgMu.Unlock()
		if inUse != "xoxe.xapp-1-OLD" {
			t.Errorf("new token in use before it was persisted: %q", inUse)
		}
		persisted, expiry = [2]string{a, r}, exp
		return nil
	})
	if _, err := s.refreshAppToken(context.Background(), "xoxe.xapp-1-OLD"); err != nil {
		t.Fatal(err)
	}
	if persisted != [2]string{"xoxe.xapp-1-NEW1", "xoxe-1-REF1"} {
		t.Fatalf("persisted %v", persisted)
	}
	if d := time.Until(expiry); d < 11*time.Hour || d > 12*time.Hour {
		t.Fatalf("expiry in %v", d)
	}
	s.cfgMu.Lock()
	cfg := s.cfg
	s.cfgMu.Unlock()
	if cfg.AppToken != "xoxe.xapp-1-NEW1" || cfg.AppRefreshToken != "xoxe-1-REF1" || cfg.AppTokenExpiresAt == "" {
		t.Fatalf("cfg after refresh = %+v", cfg)
	}
	rot := s.AppTokenRotation()
	if !rot.Rotating || rot.LastError != "" || rot.LastRefresh.IsZero() || !rot.ExpiresAt.Equal(expiry) {
		t.Fatalf("rotation = %+v", rot)
	}
	if s.tokenOrigin("xoxe.xapp-1-NEW1") != "xoxe.xapp-1-OLD" || s.tokenOrigin("xoxe-1-REF1") != "xoxe-1-OLDREF" {
		t.Fatal("rotated pair does not map back to the configured one")
	}

	// Hash stays put when the row now carries the rotated pair.
	stored := cfg
	stored.AppToken, stored.AppRefreshToken = "xoxe.xapp-1-OLD", "xoxe-1-OLDREF"
	src := &ConfigSource{ch: s, loadFn: func() (agentconfig.SlackChannelConfig, string) { return stored, "" }}
	before := src.Hash()
	stored = cfg
	if src.Hash() != before {
		t.Fatal("rotation changed the config hash (would hot-reload)")
	}
	stored.AppToken = "xoxe.xapp-1-PASTED"
	if src.Hash() == before {
		t.Fatal("a newly pasted token did not change the hash")
	}

	// A reload carrying an older token of the same lineage keeps the
	// newest pair in memory.
	old := cfg
	old.AppToken, old.AppRefreshToken = "xoxe.xapp-1-OLD", "xoxe-1-OLDREF"
	s.applyConfig(old, "")
	s.cfgMu.Lock()
	kept := s.cfg.AppToken
	s.cfgMu.Unlock()
	if kept != "xoxe.xapp-1-NEW1" {
		t.Fatalf("reload went back to the spent token: %q", kept)
	}
	s.cfgMu.Lock()
	pend := s.pending
	s.cfgMu.Unlock()
	if pend == nil || pend.access != "xoxe.xapp-1-NEW1" {
		t.Fatal("a store behind memory was not queued to be healed")
	}

	// After a restart the store holds NEW1; the next refresh keeps the
	// new instance's hash stable too.
	r := NewWithOwnerCached(cfg, "", "UBOT", "Bot", "Team")
	r.oauthURL = o.srv.URL + "/"
	rstored := cfg
	rsrc := &ConfigSource{ch: r, loadFn: func() (agentconfig.SlackChannelConfig, string) { return rstored, "" }}
	h := rsrc.Hash()
	if _, err := r.refreshAppToken(context.Background(), "xoxe.xapp-1-NEW1"); err != nil {
		t.Fatal(err)
	}
	r.cfgMu.Lock()
	rstored = r.cfg
	r.cfgMu.Unlock()
	if rsrc.Hash() != h {
		t.Fatal("hash moved after a refresh following a restart")
	}
}

// B1: a reload carrying the token in use with a new refresh token (or
// client credentials) takes the stored values, so pasting the fields one by
// one ends healthy without a restart.
func TestReloadTakesNewRefreshTokenOfSameToken(t *testing.T) {
	o := newOAuthStub(t)
	cfg := agentconfig.SlackChannelConfig{BotToken: "xoxb-test", AppToken: "xoxe.xapp-1-T0"}
	s := NewWithOwnerCached(cfg, "", "UBOT", "Bot", "Team")
	s.oauthURL = o.srv.URL + "/"
	if _, err := s.refreshAppToken(context.Background(), "xoxe.xapp-1-T0"); err == nil {
		t.Fatal("refreshed without credentials")
	}
	cfg.AppRefreshToken = "xoxe-1-R0"
	s.applyConfig(cfg, "")
	cfg.ClientID = o.srv.URL + "/b1"
	s.applyConfig(cfg, "")
	cfg.ClientSecret = "shh"
	s.applyConfig(cfg, "")
	s.cfgMu.Lock()
	got := s.cfg
	s.cfgMu.Unlock()
	if got.AppRefreshToken != "xoxe-1-R0" || got.ClientID == "" || got.ClientSecret != "shh" {
		t.Fatalf("reload dropped the pasted credentials: refresh=%v id=%v secret=%v",
			got.AppRefreshToken != "", got.ClientID != "", got.ClientSecret != "")
	}
	if e := s.AppTokenRotation().LastError; e != "" {
		t.Fatalf("stale error kept after new credentials: %q", e)
	}
	if rotated, err := s.refreshAppToken(context.Background(), "xoxe.xapp-1-T0"); err != nil || !rotated {
		t.Fatalf("refresh after pasting all fields: %v %v", rotated, err)
	}
	if f := <-o.form; f["refresh_token"] != "xoxe-1-R0" {
		t.Fatalf("refreshed with %q", f["refresh_token"])
	}
}

// S4: no expires_in in the answer → assume 12h instead of refreshing again
// at once.
func TestRefreshWithoutExpiresIn(t *testing.T) {
	o := newOAuthStub(t)
	o.noTTL = true
	s := rotatingChannel(o, "")
	if _, err := s.refreshAppToken(context.Background(), "xoxe.xapp-1-OLD"); err != nil {
		t.Fatal(err)
	}
	exp := s.AppTokenRotation().ExpiresAt
	if d := time.Until(exp); d < 11*time.Hour || d > 12*time.Hour {
		t.Fatalf("expiry in %v", d)
	}
	if d := nextRefreshDelay(exp, time.Now()); d < 10*time.Hour {
		t.Fatalf("next refresh in %v", d)
	}
}

// S1: cancelling the run mid-exchange (Reload/Stop) does not lose the pair.
func TestRefreshSurvivesCancelledRun(t *testing.T) {
	o := newOAuthStub(t)
	o.delay = 200 * time.Millisecond
	s := rotatingChannel(o, "")
	var saved atomic.Value
	s.SetPersistAppTokenFn(func(a, r string, _ time.Time) error { saved.Store(a); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	rotated, err := s.refreshAppToken(ctx, "xoxe.xapp-1-OLD")
	if err != nil || !rotated {
		t.Fatalf("refresh aborted by the cancelled run: %v %v", rotated, err)
	}
	if saved.Load() != "xoxe.xapp-1-NEW1" {
		t.Fatalf("saved %v", saved.Load())
	}
}

// S6: two instances of the same app share one refresh token; only one
// spends it, the other adopts the stored pair.
func TestRefreshSingleFlightAcrossInstances(t *testing.T) {
	o := newOAuthStub(t)
	o.delay = 50 * time.Millisecond
	var mu sync.Mutex
	store := [2]string{"xoxe.xapp-1-OLD", "xoxe-1-OLDREF"}
	var storeExp time.Time
	wire := func(c *Channel) {
		c.SetPersistAppTokenFn(func(a, r string, exp time.Time) error {
			mu.Lock()
			store, storeExp = [2]string{a, r}, exp
			mu.Unlock()
			return nil
		})
		c.SetLoadAppTokenFn(func() (string, string, time.Time, error) {
			mu.Lock()
			defer mu.Unlock()
			return store[0], store[1], storeExp, nil
		})
	}
	a, b := rotatingChannel(o, ""), rotatingChannel(o, "")
	wire(a)
	wire(b)
	var wg sync.WaitGroup
	for _, c := range []*Channel{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.refreshAppToken(context.Background(), "xoxe.xapp-1-OLD"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := o.calls.Load(); n != 1 {
		t.Fatalf("oauth.v2.access called %d times", n)
	}
	for _, c := range []*Channel{a, b} {
		c.cfgMu.Lock()
		tok, ref := c.cfg.AppToken, c.cfg.AppRefreshToken
		c.cfgMu.Unlock()
		if tok != "xoxe.xapp-1-NEW1" || ref != "xoxe-1-REF1" {
			t.Fatalf("instance left on %q", tok)
		}
	}
}

// S2: a failed save is kept pending and retried until it lands.
func TestPersistFailureIsRetried(t *testing.T) {
	o := newOAuthStub(t)
	s := rotatingChannel(o, "")
	var fails atomic.Int32
	fails.Store(2)
	var saved atomic.Value
	s.SetPersistAppTokenFn(func(a, r string, _ time.Time) error {
		if fails.Add(-1) >= 0 {
			return errors.New("db down")
		}
		saved.Store(a)
		return nil
	})
	if _, err := s.refreshAppToken(context.Background(), "xoxe.xapp-1-OLD"); err != nil {
		t.Fatal(err)
	}
	if s.flushPendingPersist() {
		t.Fatal("second save should still fail")
	}
	if !strings.Contains(s.AppTokenRotation().LastError, "db down") {
		t.Fatal("failed save not reported")
	}
	if !s.flushPendingPersist() || saved.Load() != "xoxe.xapp-1-NEW1" {
		t.Fatal("pending pair not saved on retry")
	}
	if e := s.AppTokenRotation().LastError; e != "" {
		t.Fatalf("error kept after the save landed: %q", e)
	}
}

func TestProbeAppTokenRotationDetail(t *testing.T) {
	o := newOAuthStub(t)
	now := time.Now()
	s := rotatingChannel(o, now.Add(3*time.Hour).UTC().Format(time.RFC3339))
	if hc := s.probeAppTokenRotation(s.cfg, now); !hc.OK || !strings.Contains(hc.Detail, "expires in 3h0m0s") {
		t.Fatalf("health = %+v", hc)
	}
	if hc := s.probeAppTokenRotation(s.cfg, now.Add(4*time.Hour)); !strings.Contains(hc.Detail, "expired 1h0m0s ago") {
		t.Fatalf("health = %+v", hc)
	}
}

// Saving failing keeps the new token in memory and says so.
func TestRefreshPersistFailureKeepsToken(t *testing.T) {
	o := newOAuthStub(t)
	s := rotatingChannel(o, "")
	s.SetPersistAppTokenFn(func(string, string, time.Time) error { return errors.New("db down") })
	if _, err := s.refreshAppToken(context.Background(), "xoxe.xapp-1-OLD"); err != nil {
		t.Fatal(err)
	}
	rot := s.AppTokenRotation()
	if !strings.Contains(rot.LastError, "db down") {
		t.Fatalf("last error = %q", rot.LastError)
	}
	s.cfgMu.Lock()
	tok := s.cfg.AppToken
	s.cfgMu.Unlock()
	if tok != "xoxe.xapp-1-NEW1" {
		t.Fatalf("token = %q", tok)
	}
}

// A failed refresh keeps the old token and reports a clear message.
func TestRefreshFailureRecordsError(t *testing.T) {
	o := newOAuthStub(t)
	o.fail = "invalid_refresh_token"
	s := rotatingChannel(o, "")
	if _, err := s.refreshAppToken(context.Background(), "xoxe.xapp-1-OLD"); err == nil {
		t.Fatal("want error")
	}
	rot := s.AppTokenRotation()
	if !strings.HasPrefix(rot.LastError, "Refresh failed: invalid_refresh_token.") {
		t.Fatalf("last error = %q", rot.LastError)
	}
	hc := s.probeAppTokenRotation(s.cfg, time.Now())
	if hc.OK || !strings.Contains(hc.Error, "Refresh failed") {
		t.Fatalf("health = %+v", hc)
	}
}

// Concurrent callers that saw the same token spend the refresh token once.
func TestRefreshSingleFlight(t *testing.T) {
	o := newOAuthStub(t)
	o.delay = 50 * time.Millisecond
	s := rotatingChannel(o, "")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.refreshAppToken(context.Background(), "xoxe.xapp-1-OLD")
		}()
	}
	wg.Wait()
	if n := o.calls.Load(); n != 1 {
		t.Fatalf("oauth.v2.access called %d times", n)
	}
}

func TestRefreshNeedsCredentials(t *testing.T) {
	o := newOAuthStub(t)
	s := rotatingChannel(o, "")
	s.cfg.ClientSecret = ""
	if _, err := s.refreshAppToken(context.Background(), "xoxe.xapp-1-OLD"); !errors.Is(err, errRotationIncomplete) {
		t.Fatalf("err = %v", err)
	}
	if o.calls.Load() != 0 {
		t.Fatal("refreshed without credentials")
	}
	hc := s.probeAppTokenRotation(s.cfg, time.Now())
	if hc.OK || !strings.Contains(hc.Error, "client secret") {
		t.Fatalf("health = %+v", hc)
	}
	// Legacy tokens never refresh.
	s.cfg.AppToken = "xapp-1-LEGACY"
	if _, err := s.refreshAppToken(context.Background(), "xapp-1-LEGACY"); err != nil || o.calls.Load() != 0 {
		t.Fatalf("legacy refresh: %v calls=%d", err, o.calls.Load())
	}
}

func TestNextRefreshDelay(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		exp  time.Time
		want time.Duration
	}{
		{time.Time{}, 0},
		{now.Add(12 * time.Hour), 11 * time.Hour},
		{now.Add(90 * time.Minute), 30 * time.Minute},
		{now.Add(30 * time.Minute), time.Minute},
		{now.Add(-time.Hour), time.Minute},
	} {
		if got := nextRefreshDelay(c.exp, now); got != c.want {
			t.Errorf("expires %v: got %v want %v", c.exp.Sub(now), got, c.want)
		}
	}
}

// An auth error on a rotating token refreshes once and reconnects at once,
// without waiting out the backoff.
func TestServeRefreshesOnAuthError(t *testing.T) {
	o := newOAuthStub(t)
	oldMin, oldRunner := socketRetryMin, socketRunner
	socketRetryMin = time.Hour
	var runs atomic.Int32
	socketRunner = func(ctx context.Context, c *socketmode.Client) error {
		if runs.Add(1) == 1 {
			return errors.New("token_expired")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { socketRetryMin, socketRunner = oldMin, oldRunner })

	s := rotatingChannel(o, time.Now().Add(10*time.Hour).UTC().Format(time.RFC3339))
	served := make(chan error, 1)
	go func() { served <- s.Start(context.Background()) }()
	waitFor(t, func() bool { return runs.Load() >= 2 }, "socket not reconnected after refresh")
	if n := o.calls.Load(); n != 1 {
		t.Fatalf("refreshed %d times", n)
	}
	s.Stop()
	<-served
}

// A rotating token with no known expiry is refreshed before connecting.
func TestServeRefreshesAtStartWhenExpiryUnknown(t *testing.T) {
	o := newOAuthStub(t)
	oldRunner := socketRunner
	var refreshedFirst atomic.Bool
	var runs atomic.Int32
	socketRunner = func(ctx context.Context, c *socketmode.Client) error {
		if runs.Add(1) == 1 {
			refreshedFirst.Store(o.calls.Load() == 1)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { socketRunner = oldRunner })

	s := rotatingChannel(o, "")
	served := make(chan error, 1)
	go func() { served <- s.Start(context.Background()) }()
	waitFor(t, func() bool { return runs.Load() >= 1 }, "socket never ran")
	if !refreshedFirst.Load() {
		t.Fatal("socket connected before the unknown-expiry refresh")
	}
	if s.AppTokenRotation().ExpiresAt.IsZero() {
		t.Fatal("expiry still unknown")
	}
	s.Stop()
	<-served
}

// A legacy xapp- token never calls oauth.v2.access, even on auth errors.
func TestServeLegacyTokenNeverRefreshes(t *testing.T) {
	o := newOAuthStub(t)
	oldMin, oldRunner := socketRetryMin, socketRunner
	socketRetryMin = 10 * time.Millisecond
	var runs atomic.Int32
	socketRunner = func(ctx context.Context, c *socketmode.Client) error {
		if runs.Add(1) == 1 {
			return errors.New("invalid_auth")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { socketRetryMin, socketRunner = oldMin, oldRunner })

	s := NewWithOwnerCached(agentconfig.SlackChannelConfig{BotToken: "xoxb-test", AppToken: "xapp-1-LEGACY"}, "", "UBOT", "Bot", "Team")
	s.oauthURL = o.srv.URL + "/"
	served := make(chan error, 1)
	go func() { served <- s.Start(context.Background()) }()
	waitFor(t, func() bool { return runs.Load() >= 2 }, "legacy socket not reconnected")
	if o.calls.Load() != 0 || s.AppTokenRotation().Rotating {
		t.Fatal("legacy token took the rotation path")
	}
	s.Stop()
	<-served
}

// The scheduler refreshes when due and cycles the socket onto the new token.
func TestRotateLoopRefreshesAndCyclesSocket(t *testing.T) {
	o := newOAuthStub(t)
	oldLead, oldMinDelay, oldRunner := rotateLead, rotateMinDelay, socketRunner
	rotateLead, rotateMinDelay = time.Hour, 20*time.Millisecond
	var runs atomic.Int32
	socketRunner = func(ctx context.Context, c *socketmode.Client) error {
		runs.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { rotateLead, rotateMinDelay, socketRunner = oldLead, oldMinDelay, oldRunner })

	// Due ~1s after start (RFC 3339 drops sub-seconds): not refreshed at
	// start, then by the scheduler.
	s := rotatingChannel(o, time.Now().Add(time.Hour+1500*time.Millisecond).UTC().Format(time.RFC3339))
	served := make(chan error, 1)
	go func() { served <- s.Start(context.Background()) }()
	waitFor(t, func() bool { return o.calls.Load() >= 1 && runs.Load() >= 2 }, "scheduled refresh did not cycle the socket")
	s.cfgMu.Lock()
	tok := s.cfg.AppToken
	s.cfgMu.Unlock()
	if tok != "xoxe.xapp-1-NEW1" {
		t.Fatalf("token = %q", tok)
	}
	if state, _ := s.SocketState(); state == "error" {
		t.Fatal("a planned cycle reported an error")
	}
	s.Stop()
	<-served
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// F1: a cycled socket's leftover events are read off so its runner can
// finish, but never handled (drainSocket has no handle/ack path): an ack
// through the dead client would not reach Slack, which then redelivers on
// the new socket — handling here too would answer twice.
func TestDrainSocketUnblocksRunnerWithoutHandling(t *testing.T) {
	s := NewWithOwnerCached(agentconfig.SlackChannelConfig{BotToken: "xoxb-test", AppToken: "xoxe.xapp-1-A"}, "", "UBOT", "Bot", "Team")
	s.cfgMu.Lock()
	old := s.socket
	s.cfgMu.Unlock()
	for i := range cap(old.Events) {
		old.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Request: &socketmode.Request{EnvelopeID: fmt.Sprint("env-", i)}}
	}
	done := make(chan error, 1)
	runnerOut := make(chan struct{})
	go func() { // a closing runner still delivering one more event
		old.Events <- socketmode.Event{Type: socketmode.EventTypeEventsAPI, Request: &socketmode.Request{EnvelopeID: "late"}}
		done <- nil
		close(runnerOut)
	}()
	start := time.Now()
	s.drainSocket(old, done)
	if time.Since(start) > time.Second {
		t.Fatal("drain did not end when the runner stopped")
	}
	select {
	case <-runnerOut:
	case <-time.After(time.Second):
		t.Fatal("runner stayed blocked on a full Events channel")
	}

	// A runner that already stopped: return at once.
	stopped := make(chan error, 1)
	stopped <- nil
	start = time.Now()
	s.drainSocket(newSocketClient(s.api), stopped)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("drain waited for a stopped runner")
	}
}

// An ack on a client whose runner is gone gives up instead of blocking
// serve (and with it Reload) forever once slack-go's queue is full.
func TestAckNeverBlocks(t *testing.T) {
	old := socketAckTimeout
	socketAckTimeout = 20 * time.Millisecond
	t.Cleanup(func() { socketAckTimeout = old })
	s := NewWithOwnerCached(agentconfig.SlackChannelConfig{BotToken: "xoxb-test", AppToken: "xapp-1-A"}, "", "UBOT", "Bot", "Team")
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := range 40 { // queue holds 20, nobody reads it
			s.ack(socketmode.Event{Request: &socketmode.Request{EnvelopeID: fmt.Sprint("env-", i)}})
		}
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("ack blocked on a full queue")
	}
}

// N-g: while a save is pending the store is behind; its pair is never
// adopted even when the alias that would mark it ours was evicted.
func TestAdoptStoredSkippedWhilePending(t *testing.T) {
	o := newOAuthStub(t)
	s := rotatingChannel(o, "")
	s.cfgMu.Lock()
	s.pending = &rotatedPair{access: "xoxe.xapp-1-OLD", refresh: "xoxe-1-OLDREF"}
	s.cfgMu.Unlock()
	if s.adoptStored("xoxe.xapp-1-STALE", "xoxe-1-STALEREF", time.Now().Add(time.Hour)) {
		t.Fatal("adopted the store's pair while it is behind")
	}
}
