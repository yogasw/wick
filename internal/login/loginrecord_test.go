package login

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

func countSessions(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&entity.Session{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// Wick's own sessions are stateless, so nothing was ever written down and
// the admin page could only ever say "never signed in" — about everybody,
// forever. This is the row that fixes that.
func TestRecordLoginWritesTheSignIn(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	ctx := context.Background()

	svc.RecordLogin(ctx, "u-1")
	svc.RecordLogin(ctx, "u-1")
	svc.RecordLogin(ctx, "u-2")

	if got := countSessions(t, db); got != 3 {
		t.Fatalf("%d records, want one per sign-in", got)
	}
	var rows []entity.Session
	if err := db.Where("user_id = ?", "u-1").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows for u-1, want 2", len(rows))
	}
	// Each row is its own record; a reused key would collapse a history
	// into a single line.
	if rows[0].Token == rows[1].Token {
		t.Error("two sign-ins share one id — the second would overwrite the first")
	}
	for _, r := range rows {
		if r.CreatedAt.IsZero() || !r.ExpiresAt.After(r.CreatedAt) {
			t.Errorf("row = %+v, want a stamp and a future expiry", r)
		}
	}
}

// The row is an audit record, not a credential. Writing the cookie down
// would turn a history into a list of live keys.
func TestRecordLoginStoresNoCredential(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	svc.RecordLogin(context.Background(), "u-1")

	var row entity.Session
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	cookie, err := encryptSession("a-secret-value-for-the-test", "u-1", nil, sessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	if row.Token == cookie {
		t.Fatal("the session cookie was written to the database")
	}
	if len(row.Token) != 32 {
		t.Errorf("id = %q, want a random 16-byte hex id", row.Token)
	}
}

// Nothing else in wick deletes from this table, so an insert with no
// matching delete grows it forever. Each write drops that user's expired
// history — bounded work, no new scheduler.
func TestRecordLoginPrunesOldHistory(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	ctx := context.Background()
	old := time.Now().UTC().Add(-loginHistoryKeep - 24*time.Hour)

	for _, r := range []entity.Session{
		{Token: "ancient-1", UserID: "u-1", CreatedAt: old, ExpiresAt: old.Add(time.Hour)},
		{Token: "ancient-2", UserID: "u-2", CreatedAt: old, ExpiresAt: old.Add(time.Hour)},
	} {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc.RecordLogin(ctx, "u-1")

	var left []entity.Session
	if err := db.Find(&left).Error; err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Fatalf("%d rows left: %+v", len(left), left)
	}
	for _, r := range left {
		if r.Token == "ancient-1" {
			t.Error("u-1's expired history survived their own sign-in")
		}
	}
	// Somebody else's history is not this login's business to delete —
	// the work must stay proportional to the sign-in that triggered it.
	found := false
	for _, r := range left {
		if r.Token == "ancient-2" {
			found = true
		}
	}
	if !found {
		t.Error("pruning reached beyond the user who signed in")
	}
}

// A sign-in must never fail because bookkeeping did. The person is
// already authenticated by the time this runs.
func TestRecordLoginNeverPanicsOnABrokenStore(t *testing.T) {
	db := newLoginSQLite(t)
	svc := NewService(db, "")
	if err := db.Migrator().DropTable(&entity.Session{}); err != nil {
		t.Fatal(err)
	}
	svc.RecordLogin(context.Background(), "u-1") // must be a no-op, not a crash

	// And the degenerate inputs a caller might hand it.
	svc.RecordLogin(context.Background(), "")
	var nilSvc *Service
	nilSvc.RecordLogin(context.Background(), "u-1")
}
