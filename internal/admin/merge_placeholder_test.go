package admin

import (
	"testing"

	"github.com/yogasw/wick/internal/channelidentity"
)

// TestChannelPlaceholderDomainMatches pins the duplicated suffix. If the two
// drift, the users page stops offering a merge for a Telegram account — an admin
// would approve it standalone and the person ends up with two accounts.
func TestChannelPlaceholderDomainMatches(t *testing.T) {
	sample := channelidentity.PlaceholderEmail("telegram", "555")
	if !isChannelPlaceholderEmail(sample) {
		t.Fatalf("admin does not recognise %q as a placeholder; the domain constants drifted", sample)
	}
	if isChannelPlaceholderEmail("ada@example.com") {
		t.Error("a real address was treated as a placeholder")
	}
}
