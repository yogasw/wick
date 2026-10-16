package config

import (
	"strings"
	"testing"

	pkgentity "github.com/yogasw/wick/pkg/entity"
)

// The rotation fields sit in their own Token Rotation card, away from the
// Connection card most users fill, and the expiry wick writes is internal.
func TestSlackTokenRotationLayout(t *testing.T) {
	byKey := map[string]pkgentity.Config{}
	for _, r := range pkgentity.StructToConfigs(SlackChannelConfig{}) {
		byKey[r.Key] = r
	}
	for _, k := range []string{"app_refresh_token", "client_id", "client_secret", "app_token_expires_at"} {
		if title, _, _ := strings.Cut(byKey[k].Group, "|"); title != "Token Rotation" {
			t.Errorf("%s in group %q, want Token Rotation", k, title)
		}
	}
	if !byKey["app_token_expires_at"].Internal {
		t.Error("app_token_expires_at is not internal")
	}
	for k, r := range byKey {
		if r.Internal && k != "app_token_expires_at" {
			t.Errorf("%s is internal, only the expiry should be", k)
		}
	}
	if g := byKey["mode"].Group; !strings.HasPrefix(g, "Connection|Transport credentials.") {
		t.Errorf("Connection group changed: %q", g)
	}
}
