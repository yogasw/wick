package connectors

import "testing"

func TestIsAuthError(t *testing.T) {
	yes := []string{
		"refresh error: invalid_grant: Token has been expired or revoked.",
		"googleapis 401: {\"error\":{\"status\":\"UNAUTHENTICATED\"}}",
		"googleapis 403: Method doesn't allow unregistered callers",
		"unauthorized after token refresh",
		"slack: invalid_auth",
		"token_revoked",
	}
	for _, m := range yes {
		if !IsAuthError(m) {
			t.Errorf("IsAuthError(%q) = false, want true", m)
		}
	}
	no := []string{
		"googleapis 403: The caller does not have permission",
		"googleapis 404: File not found",
		"channel_not_found",
		"request: context deadline exceeded",
		"",
	}
	for _, m := range no {
		if IsAuthError(m) {
			t.Errorf("IsAuthError(%q) = true, want false", m)
		}
	}
}
