// Package slack — oauth.go: OAuthMeta implementation for the Slack connector.
//
// Purpose: Provides the OAuthMeta descriptor that wires the Slack user OAuth
// flow into the generic connector manager OAuth framework. When Module.OAuth
// is non-nil the manager UI automatically renders the "OAuth App" section and
// "Connect" button without any Slack-specific handler code.
//
// Caller:   Module() in internal/connectors/slack (set Module.OAuth = SlackOAuthMeta())
// Dependencies: slack-go
// Main Functions:
//   - SlackOAuthMeta() — returns the OAuthMeta for Slack user-token acquisition
//   - userOAuthScopes() — derives the consent scope list from opScopes
//
// Side Effects: none (pure descriptor, no side effects at init time).
package slack

import (
	"context"
	"sort"
	"strings"

	slackgo "github.com/slack-go/slack"
	"github.com/yogasw/wick/pkg/connector"
)

// extraUserScopes are scopes no entry in opScopes declares, because no
// operation calls the method that needs them: send.go opens a DM with
// conversations.open before posting, which wants im:write (mpim:write for
// the group-DM form). Without them a user token can post into a channel
// but not start a conversation.
var extraUserScopes = []string{"im:write", "mpim:write"}

// userOAuthScopes returns the comma-separated user_scope for the consent
// URL, derived from opScopes rather than written out a second time — a
// hand-kept list drifts, and the drift is invisible until someone hits
// missing_scope on an operation the connector advertises. Slack grants
// exactly what the authorize URL asks for (unioned with whatever that
// user already granted the app), so anything absent here is unreachable
// no matter how often the account is reconnected.
//
// Every alternative inside an any-of group is requested, not just the
// first: get_channel_history needs channels:history on a public channel
// and im:history in a DM, and a token holding only one of the two fails
// the other case.
//
// Slack rejects the whole authorize URL with invalid_scope when one of
// these is not declared in the app's User Token Scopes, so an instance
// can narrow the list via its oauth_user_scopes config without a build.
func userOAuthScopes() string {
	set := make(map[string]struct{}, 32)
	for _, groups := range opScopes {
		for _, anyOf := range groups {
			for _, scope := range anyOf {
				set[scope] = struct{}{}
			}
		}
	}
	for _, scope := range extraUserScopes {
		set[scope] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for scope := range set {
		out = append(out, scope)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// SlackOAuthMeta returns the OAuthMeta descriptor for Slack user token OAuth.
// The generic manager oauth handler reads AuthorizeURL and Scopes to build
// the consent redirect, and calls GetUserIdentity after the code exchange to
// resolve which connector row to update.
func SlackOAuthMeta() *connector.OAuthMeta {
	return &connector.OAuthMeta{
		AuthorizeURL: "https://slack.com/oauth/v2/authorize",
		Scopes:        userOAuthScopes(),
		ResolveScopes: resolveUserScopesFromManifest,
		DisplayName:  "Slack",
		Icon: `<svg width="16" height="16" viewBox="0 0 122.8 122.8" fill="none" xmlns="http://www.w3.org/2000/svg">` +
			`<path d="M25.8 77.6c0 7.1-5.8 12.9-12.9 12.9S0 84.7 0 77.6s5.8-12.9 12.9-12.9h12.9v12.9zm6.5 0c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9v32.3c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V77.6z" fill="#E01E5A"/>` +
			`<path d="M45.2 25.8c-7.1 0-12.9-5.8-12.9-12.9S38.1 0 45.2 0s12.9 5.8 12.9 12.9v12.9H45.2zm0 6.5c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H12.9C5.8 58.1 0 52.3 0 45.2s5.8-12.9 12.9-12.9h32.3z" fill="#36C5F0"/>` +
			`<path d="M97 45.2c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9-5.8 12.9-12.9 12.9H97V45.2zm-6.5 0c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V12.9C64.7 5.8 70.5 0 77.6 0s12.9 5.8 12.9 12.9v32.3z" fill="#2EB67D"/>` +
			`<path d="M77.6 97c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9-12.9-5.8-12.9-12.9V97h12.9zm0-6.5c-7.1 0-12.9-5.8-12.9-12.9s5.8-12.9 12.9-12.9h32.3c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H77.6z" fill="#ECB22E"/>` +
			`</svg>`,
		GetUserIdentity: func(ctx context.Context, accessToken string) (userID, displayName string, err error) {
			api := slackgo.New(accessToken)
			resp, err := api.AuthTestContext(ctx)
			if err != nil {
				return "", "", err
			}
			name := resp.User
			if name == "" {
				name = resp.UserID
			}
			return resp.UserID, name, nil
		},
	}
}
