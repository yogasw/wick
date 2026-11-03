package connectors

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// tokenServer is an OAuth token endpoint that mints "fresh-N" per refresh.
type tokenServer struct {
	*httptest.Server
	refreshes atomic.Int32
	fail      atomic.Bool
}

func newTokenServer(t *testing.T) *tokenServer {
	t.Helper()
	ts := &tokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		if ts.fail.Load() {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"revoked"}`)
			return
		}
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_request"}`)
			return
		}
		n := ts.refreshes.Add(1)
		fmt.Fprintf(w, `{"access_token":"fresh-%d","expires_in":3599}`, n)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func refreshModule(tokenURL string) connector.Module {
	echo := func(c *connector.Ctx) (any, error) { return "token=" + c.Cfg("user_token"), nil }
	return connector.Module{
		Meta: connector.Meta{Key: "acme-oauth", Name: "Acme OAuth", Description: "oauth refresh test"},
		Configs: []entity.Config{
			{Key: "client_id", Type: "text"},
			{Key: "client_secret", Type: "text", IsSecret: true},
			{Key: "user_token", Type: "text", IsSecret: true},
			{Key: "refresh_token", Type: "text", IsSecret: true},
		},
		Operations: []connector.Category{
			connector.Cat("", "", connector.Op("whoami", "Who am I", "echo the token", struct{}{}, echo, wickdocs.Docs{})),
		},
		OAuth: &connector.OAuthMeta{DisplayName: "Acme", TokenURL: tokenURL},
	}
}

func newSvcRefresh(t *testing.T, tokenURL string) *Service {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	require.NoError(t, cfgsSvc.Bootstrap(context.Background()))
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	require.NoError(t, svc.Bootstrap(context.Background(), []connector.Module{refreshModule(tokenURL)}))
	return svc
}

func runAs(t *testing.T, svc *Service, row entity.Connector, userID string) string {
	t.Helper()
	res, err := svc.Execute(context.Background(), ExecuteParams{
		ConnectorID:  row.ID,
		OperationKey: "whoami",
		Input:        map[string]string{},
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       userID,
	})
	require.NoError(t, err)
	return res.ResponseJSON
}

// The instance credential (the default path) starts with only a refresh
// token: the first call mints and stores one, later calls reuse it.
func TestOAuthRefreshInstanceTokenMintedOnceAndStored(t *testing.T) {
	ts := newTokenServer(t)
	svc := newSvcRefresh(t, ts.URL)
	row, err := svc.Create(context.Background(), "acme-oauth", "Team", map[string]string{
		"client_id": "cid", "client_secret": "secret", "refresh_token": "rt-instance",
	}, "u-owner")
	require.NoError(t, err)

	require.Contains(t, runAs(t, svc, *row, "u-alice"), "fresh-1")
	require.Contains(t, runAs(t, svc, *row, "u-alice"), "fresh-1")
	require.EqualValues(t, 1, ts.refreshes.Load(), "the second call reuses the stored token")
	require.Equal(t, "fresh-1", svc.LoadConfigs(*row)["user_token"], "the token is written back to the instance")
}

// An account connected without a known expiry is refreshed once; the new
// token and its expiry land on the account row.
func TestOAuthRefreshAccountTokenSavedWithExpiry(t *testing.T) {
	ts := newTokenServer(t)
	svc := newSvcRefresh(t, ts.URL)
	ctx := context.Background()
	row, err := svc.Create(ctx, "acme-oauth", "Team", map[string]string{"client_id": "cid", "client_secret": "secret"}, "u-owner")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, AccessPolicy{EnableSSO: true, MultiAccount: true}))
	require.NoError(t, svc.repo.SetSharedCredentialPolicy(ctx, row.ID, false))
	require.NoError(t, svc.SaveAccountTokens(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-old", "rt-alice", nil))
	fresh, err := svc.Get(ctx, row.ID)
	require.NoError(t, err)

	require.Contains(t, runAs(t, svc, *fresh, "u-alice"), "fresh-1")
	require.Contains(t, runAs(t, svc, *fresh, "u-alice"), "fresh-1")
	require.EqualValues(t, 1, ts.refreshes.Load())

	acct, err := svc.OwnAccount(ctx, row.ID, "u-alice")
	require.NoError(t, err)
	require.Equal(t, "fresh-1", acct.AccessToken)
	require.NotNil(t, acct.ExpiresAt)
	require.WithinDuration(t, time.Now().Add(time.Hour), *acct.ExpiresAt, 2*time.Minute)
}

// A token that is still valid is used as is.
func TestOAuthRefreshSkipsValidAccountToken(t *testing.T) {
	ts := newTokenServer(t)
	svc := newSvcRefresh(t, ts.URL)
	ctx := context.Background()
	row, err := svc.Create(ctx, "acme-oauth", "Team", map[string]string{"client_id": "cid", "client_secret": "secret"}, "u-owner")
	require.NoError(t, err)
	require.NoError(t, svc.SetAccessPolicy(ctx, row.ID, AccessPolicy{EnableSSO: true}))
	require.NoError(t, svc.repo.SetSharedCredentialPolicy(ctx, row.ID, false))
	exp := time.Now().Add(30 * time.Minute)
	require.NoError(t, svc.SaveAccountTokens(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-valid", "rt-alice", &exp))
	fresh, err := svc.Get(ctx, row.ID)
	require.NoError(t, err)

	require.Contains(t, runAs(t, svc, *fresh, "u-alice"), "tok-valid")
	require.EqualValues(t, 0, ts.refreshes.Load())
}

// A refused refresh does not fail the call: the op runs on the stored token
// exactly as it did before core refreshed anything.
func TestOAuthRefreshFailureFallsBackToStoredToken(t *testing.T) {
	ts := newTokenServer(t)
	ts.fail.Store(true)
	svc := newSvcRefresh(t, ts.URL)
	row, err := svc.Create(context.Background(), "acme-oauth", "Team", map[string]string{
		"client_id": "cid", "client_secret": "secret", "user_token": "tok-stored", "refresh_token": "rt-instance",
	}, "u-owner")
	require.NoError(t, err)

	require.Contains(t, runAs(t, svc, *row, "u-alice"), "tok-stored")
	require.Equal(t, "tok-stored", svc.LoadConfigs(*row)["user_token"], "nothing is overwritten on failure")
}

// Without a TokenURL (Slack's own flow) or without a refresh token nothing
// is refreshed.
func TestOAuthRefreshNeedsTokenURLAndRefreshToken(t *testing.T) {
	ts := newTokenServer(t)
	svc := newSvcRefresh(t, ts.URL)
	row, err := svc.Create(context.Background(), "acme-oauth", "Team", map[string]string{
		"client_id": "cid", "client_secret": "secret", "user_token": "tok-stored",
	}, "u-owner")
	require.NoError(t, err)
	require.Contains(t, runAs(t, svc, *row, "u-alice"), "tok-stored")

	mod := refreshModule("")
	cfg := map[string]string{"client_id": "cid", "refresh_token": "rt", "user_token": "tok"}
	require.Empty(t, svc.ensureOAuthToken(context.Background(), mod, *row, nil, cfg))
	require.Equal(t, "tok", cfg["user_token"])
	require.EqualValues(t, 0, ts.refreshes.Load())
}
