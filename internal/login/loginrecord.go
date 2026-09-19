package login

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/entity"
)

// loginrecord.go writes down that somebody signed in.
//
// Wick's own sessions are STATELESS: SetSessionCookie encrypts
// {userID, tagIDs} into a cookie and nothing is stored, which is a good
// design — no lookup on every request, nothing to clean up, no session
// store to keep consistent. The cost is that there was no record of a
// sign-in anywhere, so the admin page's "last login" column could only
// ever say "never" for everybody, for every account, forever.
//
// This adds the record and nothing else. Three properties matter, and
// each of them is a deliberate limit rather than an omission:
//
//  1. It is an AUDIT row, not a session. Nothing authenticates against
//     it, so deleting the whole table logs nobody out and a failed write
//     costs only the record. If a later change makes the cookie depend
//     on a row here, that is a new design — not an extension of this one.
//  2. It is best-effort. A person signing in must never be turned away
//     because a bookkeeping insert failed; the error is logged and the
//     login proceeds.
//  3. It cleans up after itself. Nothing else in wick deletes from this
//     table, so an insert with no matching delete would grow it forever.
//     Each write drops that user's expired rows — bounded work, no new
//     scheduler, and the table stays roughly "live sessions plus recent
//     history".
//
// No schema change is involved: entity.Session is already in the
// migration set and the table already exists, empty. That is the whole
// reason this shape was chosen over a new table — a fresh AutoMigrate on
// a live database is exactly the kind of change that bites (a new column
// leaves every pooled connection holding a stale cached plan).
//
// The token column stores a random id, never the cookie. The cookie is a
// credential; writing it down would turn an audit log into a list of
// live keys.

// loginHistoryKeep is how long a recorded sign-in stays readable after
// the session it describes has expired. Long enough for "who has been
// using this lately", short enough that the table cannot grow without
// bound on a busy install.
const loginHistoryKeep = 90 * 24 * time.Hour

// RecordLogin notes that userID signed in, for the admin analytics.
// Never blocks, never fails the caller: a sign-in is the user's, the
// record is ours.
func (s *Service) RecordLogin(ctx context.Context, userID string) {
	if s == nil || s.repo == nil || userID == "" {
		return
	}
	id, err := randomID()
	if err != nil {
		log.Warn().Err(err).Msg("login: could not generate an id for the sign-in record")
		return
	}
	now := time.Now().UTC()
	row := entity.Session{
		Token:     id,
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}
	if err := s.repo.db.WithContext(ctx).Create(&row).Error; err != nil {
		// Deliberately not returned: the person is already signed in, and
		// refusing them over a missing audit row would be the worse bug.
		log.Warn().Err(err).Str("user_id", userID).Msg("login: sign-in not recorded")
		return
	}
	s.repo.pruneLoginHistory(ctx, userID, now.Add(-loginHistoryKeep))
}

// pruneLoginHistory drops this user's records older than cutoff. Scoped
// to one user so the work stays proportional to the login that triggered
// it rather than to the size of the table.
func (r *repo) pruneLoginHistory(ctx context.Context, userID string, cutoff time.Time) {
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND created_at < ?", userID, cutoff).
		Delete(&entity.Session{}).Error; err != nil {
		log.Debug().Err(err).Msg("login: could not prune old sign-in records")
	}
}

// randomID is the row's primary key. It identifies the record, not the
// session — there is no way to go from it to a cookie, and that is the
// point.
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
