package delegation

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
)

// Moving existing roles onto the background default.
//
// Every role created before this change was stored as foreground, because
// foreground was the only thing create_agent ever wrote — the field was
// never a decision anybody made. Flipping only the column default would
// leave those rows blocking forever, so the roles a team already uses
// would keep holding their leader's process while the new default applied
// to nothing.
//
// So the stored rows move too, once, guarded by a marker. Two exceptions
// stay foreground: the seeded roles that answer FOR the leader rather than
// for a human — a checker's verdict and a customer draft are what the very
// next sentence is about, and waking the leader to read them buys nothing.
//
// A role an operator has deliberately set to foreground since is not
// distinguishable from one that never chose, so it moves as well. That is
// the cost of the flip, and it is recoverable in one edit; the alternative
// is a default nobody's existing roles obey.

// ModeMigrationMarkerKey records that the flip has run.
const ModeMigrationMarkerKey = "sub_agents_background_default_v1"

// foregroundRoleKeys are the seeded roles that keep blocking their caller.
var foregroundRoleKeys = []string{"evidence-checker", "client-response-drafter"}

// MigrateModeDefault switches pre-existing roles to the background mode.
//
// Idempotent by marker: it runs once per install and never re-runs, so an
// operator who sets a role back to foreground keeps that setting across
// restarts.
func MigrateModeDefault(ctx context.Context, repo *Repo, marker SeedMarker) error {
	if repo == nil {
		return nil
	}
	if marker != nil && strings.TrimSpace(marker.Get()) != "" {
		return nil
	}
	n, err := repo.SwitchProfilesToBackground(ctx, foregroundRoleKeys)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Info().Int64("roles", n).
			Msg("delegation: existing roles switched to the background default")
	}
	if marker != nil {
		return marker.Set("true")
	}
	return nil
}
