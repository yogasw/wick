package admin

import (
	"testing"

	"github.com/yogasw/wick/internal/login"
)

// TestImpersonateCookieNameMatchesMiddleware pins the duplicated constant.
// internal/admin imports internal/login, so login cannot import back and repeats
// the cookie name locally. If the two drift, the banner silently stops appearing
// and an admin can act as another user with no warning on screen.
func TestImpersonateCookieNameMatchesMiddleware(t *testing.T) {
	if got := login.ImpersonatorCookieNameForTest(); got != impersonateCookie {
		t.Fatalf("login copy = %q, admin constant = %q — keep them equal", got, impersonateCookie)
	}
}
