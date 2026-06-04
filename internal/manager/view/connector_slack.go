package view

import "github.com/yogasw/wick/internal/entity"

// oauthConnectedUser returns the connected username shown on the OAuth Connect
// button. Reads from the "connected_user" synthetic config key written by the
// OAuth callback, which records the display name resolved via GetUserIdentity.
func oauthConnectedUser(configs []entity.Config) string {
	for _, c := range configs {
		if c.Key == "connected_user" {
			return c.Value
		}
	}
	return ""
}
