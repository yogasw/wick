package view

import "github.com/yogasw/wick/internal/entity"

// slackOAuthReady returns true when this connector row has auth_mode=user_token.
// The "Connect with Slack" button is shown when the channel has client_id
// configured (passed as oauthURL being non-empty).
func slackOAuthReady(configs []entity.Config) bool {
	for _, c := range configs {
		if c.Key == "auth_mode" && c.Value == "user_token" {
			return true
		}
	}
	return false
}

// slackConnectedUser returns the Slack username shown in the button label.
// Reads from a synthetic "connected_user" config key written by the OAuth
// callback (not a real Slack API field).
func slackConnectedUser(configs []entity.Config) string {
	for _, c := range configs {
		if c.Key == "connected_user" {
			return c.Value
		}
	}
	return ""
}
