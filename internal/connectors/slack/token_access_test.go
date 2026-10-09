package slack

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/pkg/connector"
)

// Fake token values. They only need the right prefix; the tests assert
// none of them ever reaches the rendered HTML.
const (
	fakeBot  = "xoxb-1111-secretbotvalue"
	fakeUser = "xoxp-2222-secretuservalue"
	fakeBad  = "xoxb-3333-revokedvalue"
)

// authStub answers auth.test per bearer token: bot, user, or invalid_auth.
func authStub(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
		case fakeBot:
			w.Header().Set("X-OAuth-Scopes", "channels:read,chat:write,users:read")
			_, _ = w.Write([]byte(`{"ok":true,"team":"Acme","user":"wickbot","user_id":"UBOT1","team_id":"T1","bot_id":"B1"}`))
		case fakeUser:
			w.Header().Set("X-OAuth-Scopes", "channels:read,chat:write")
			_, _ = w.Write([]byte(`{"ok":true,"team":"Acme","user":"jane.doe","user_id":"UJANE","team_id":"T1"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
		}
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)
}

func renderPanel(t *testing.T, cfg map[string]string, accounts ...connector.AccountRef) string {
	t.Helper()
	c := newCtx(t, cfg)
	c.SetAccountsLister(func() []connector.AccountRef { return accounts })
	out, err := tokenAccess(c)
	require.NoError(t, err)
	h, _ := out.(map[string]any)["html"].(string)
	require.NotEmpty(t, h)
	for _, secret := range []string{fakeBot, fakeUser, fakeBad, "secretbotvalue", "secretuservalue", "revokedvalue"} {
		assert.NotContains(t, h, secret, "token value leaked into the panel")
	}
	return h
}

func TestTokenAccess_BotTokenInBotField(t *testing.T) {
	authStub(t)
	h := renderPanel(t, map[string]string{"auth_mode": "bot_token", "bot_token": fakeBot, "user_token": fakeUser},
		connector.AccountRef{ID: "acc1", DisplayName: "jane"})

	assert.Contains(t, h, "Operations on this instance run as <b>wickbot (UBOT1)</b> — a bot token")
	assert.NotContains(t, h, "wrong type")
	assert.Contains(t, h, "xoxb- (bot token)")
	assert.Contains(t, h, "xoxp- (user token)")
	assert.Contains(t, h, "Token in use (bot_token)")
	assert.Contains(t, h, "<b>@jane</b>")
	assert.Contains(t, h, `data-op="token_access"`, "Recheck link re-runs the op")
	// Missing scopes are named per op; custom_api_call is not judged.
	assert.Contains(t, h, "needs scope: reactions:write")
	assert.Contains(t, h, "custom_api_call")
	assert.Contains(t, h, "depends on the method called")
	// The config-only op itself is not an access row.
	assert.NotContains(t, h, `<td class="op">token_access</td>`)
}

func TestTokenAccess_UserTokenInBotFieldWarns(t *testing.T) {
	authStub(t)
	h := renderPanel(t, map[string]string{"auth_mode": "bot_token", "bot_token": fakeUser})

	assert.Contains(t, h, "wrong type")
	assert.Contains(t, h, "Operations on this instance run as <b>jane.doe (UJANE)</b> — a <b>user</b> token, although auth_mode is <code>bot_token</code>")
	assert.Contains(t, h, "it currently posts as jane.doe (UJANE)")
	assert.Contains(t, h, "— (not a bot)")
}

func TestTokenAccess_UnsetUserToken(t *testing.T) {
	authStub(t)
	h := renderPanel(t, map[string]string{"auth_mode": "bot_token", "bot_token": fakeBot})

	assert.Contains(t, h, "not set")
	assert.Contains(t, h, "Not set.")
	assert.Contains(t, h, "none connected.")
}

func TestTokenAccess_UnsetActiveToken(t *testing.T) {
	authStub(t)
	h := renderPanel(t, map[string]string{"auth_mode": "user_token", "bot_token": fakeBot})

	assert.Contains(t, h, "auth_mode is <code>user_token</code> but <code>user_token</code> is not set")
}

func TestTokenAccess_AuthTestErrorStaysInItsCard(t *testing.T) {
	authStub(t)
	h := renderPanel(t, map[string]string{"auth_mode": "user_token", "bot_token": fakeBad, "user_token": fakeUser})

	// The bad bot token shows its error; the user token in use still checks out.
	assert.Contains(t, h, "auth.test failed: slack auth.test: invalid_auth")
	assert.Contains(t, h, "Operations on this instance run as <b>jane.doe (UJANE)</b> — a user token")
	assert.Contains(t, h, "Token in use (user_token)")
}

func TestTokenAccess_ActiveTokenErrorInBanner(t *testing.T) {
	authStub(t)
	h := renderPanel(t, map[string]string{"auth_mode": "bot_token", "bot_token": fakeBad})

	assert.Contains(t, h, "The token in use (<code>bot_token</code>) failed auth.test: slack auth.test: invalid_auth")
}

func TestTokenAccess_FullyPassingCategoryCollapses(t *testing.T) {
	authStub(t)
	h := renderPanel(t, map[string]string{"auth_mode": "bot_token", "bot_token": fakeBot})

	// The bot stub holds channels:read only for the read side of Channels,
	// so history ops fail and the category expands …
	assert.Contains(t, h, `<td class="op">get_channel_history</td>`)
	// … while Users (users:read covers all three) collapses to one row.
	assert.Contains(t, h, "<td>Users</td><td>✅ 3/3</td>")
	assert.NotContains(t, h, `<td class="op">list_users</td>`)
}
