package channelidentity

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

type fakeCreator struct {
	created []string
	nextID  int
	byEmail map[string]string
}

func (f *fakeCreator) CreateForChannel(_ context.Context, email, _, _ string) (string, error) {
	if f.byEmail == nil {
		f.byEmail = map[string]string{}
	}
	if id, ok := f.byEmail[email]; ok {
		return id, nil // idempotent on email, like the real path
	}
	f.nextID++
	id := "u" + string(rune('0'+f.nextID))
	f.byEmail[email] = id
	f.created = append(f.created, email)
	return id, nil
}

// TestEmaillessResolve_CreatesOnceThenReuses is the core requirement for a
// channel with no email: the first message creates an account, and every message
// after it resolves to the SAME one. Keying on the external id is what makes
// that possible — there is no email to look up.
func TestEmaillessResolve_CreatesOnceThenReuses(t *testing.T) {
	st := NewStore(newDB(t))
	cr := &fakeCreator{}
	r := &EmaillessResolver{Store: st, Accounts: cr}
	ctx := context.Background()

	first, err := r.Resolve(ctx, "telegram", "default", "555", "Ada TG")
	if err != nil || first == "" {
		t.Fatalf("first resolve = (%q, %v)", first, err)
	}
	for i := 0; i < 3; i++ {
		again, err := r.Resolve(ctx, "telegram", "default", "555", "Ada TG")
		if err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
		if again != first {
			t.Fatalf("resolve %d returned %q, want the same account %q", i, again, first)
		}
	}
	if len(cr.created) != 1 {
		t.Fatalf("created %v accounts, want exactly 1", cr.created)
	}
}

// TestEmaillessResolve_UsesAPlaceholderEmail: entity.User.Email is unique and
// NOT NULL, so an account still needs a value. It must be obviously synthetic —
// a plausible invented address could later collide with the person's real one.
func TestEmaillessResolve_UsesAPlaceholderEmail(t *testing.T) {
	st := NewStore(newDB(t))
	cr := &fakeCreator{}
	r := &EmaillessResolver{Store: st, Accounts: cr}

	if _, err := r.Resolve(context.Background(), "telegram", "default", "555", "Ada"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(cr.created) != 1 || !IsPlaceholderEmail(cr.created[0]) {
		t.Fatalf("created email %v, want a placeholder", cr.created)
	}
}

// TestEmaillessResolve_SeparatesSenders: two Telegram users are two accounts.
func TestEmaillessResolve_SeparatesSenders(t *testing.T) {
	st := NewStore(newDB(t))
	cr := &fakeCreator{}
	r := &EmaillessResolver{Store: st, Accounts: cr}
	ctx := context.Background()

	a, _ := r.Resolve(ctx, "telegram", "default", "111", "A")
	b, _ := r.Resolve(ctx, "telegram", "default", "222", "B")
	if a == b || a == "" || b == "" {
		t.Fatalf("two senders collapsed: %q vs %q", a, b)
	}
}

// TestEmaillessResolve_RecordsTheLink: the connection must exist afterwards,
// otherwise the next message cannot find the account and would create another.
func TestEmaillessResolve_RecordsTheLink(t *testing.T) {
	st := NewStore(newDB(t))
	r := &EmaillessResolver{Store: st, Accounts: &fakeCreator{}}
	ctx := context.Background()

	id, err := r.Resolve(ctx, "telegram", "default", "555", "Ada")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rows, err := st.ListForUser(ctx, id)
	if err != nil || len(rows) != 1 {
		t.Fatalf("links = %+v (err %v), want 1", rows, err)
	}
	if rows[0].ChannelType != "telegram" || rows[0].ExternalUserID != "555" {
		t.Fatalf("unexpected link: %+v", rows[0])
	}
	// EmailAtLink stays empty: this path exists because the channel reports no
	// email, and storing the placeholder would dress a fake address up as a match.
	if rows[0].EmailAtLink != "" {
		t.Errorf("EmailAtLink = %q, want empty for an email-less channel", rows[0].EmailAtLink)
	}
}

// TestEmaillessResolve_NilSafe: a partially wired resolver returns nothing
// rather than panicking on a live message path.
func TestEmaillessResolve_NilSafe(t *testing.T) {
	var r *EmaillessResolver
	if id, err := r.Resolve(context.Background(), "telegram", "d", "1", "x"); id != "" || err != nil {
		t.Fatalf("nil resolver = (%q, %v)", id, err)
	}
	bare := &EmaillessResolver{}
	if id, _ := bare.Resolve(context.Background(), "telegram", "d", "1", "x"); id != "" {
		t.Fatalf("unwired resolver returned %q", id)
	}
}

// TestEmaillessResolve_MergeableAfterwards ties the two halves together: the
// account this creates is exactly what shows up as a merge candidate.
func TestEmaillessResolve_MergeableAfterwards(t *testing.T) {
	db := newDB(t)
	st := NewStore(db)
	ctx := context.Background()

	// Real account the person already has.
	seedUser(t, db, "real", true, entity.RoleUser)

	// Their Telegram arrives as its own pending account.
	tgID := "tg-user"
	if err := db.Create(&entity.User{ID: tgID,
		Email: PlaceholderEmail("telegram", "555"), Name: "Ada TG"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.Link(ctx, identityFor(tgID, "telegram", "default", "555", "Ada TG")); err != nil {
		t.Fatalf("link: %v", err)
	}

	cands, err := st.ListMergeCandidates(ctx)
	if err != nil || len(cands) != 1 || cands[0].User.ID != tgID {
		t.Fatalf("candidates = %+v (err %v)", cands, err)
	}
	if _, err := st.Merge(ctx, tgID, "real"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	rows, _ := st.ListForUser(ctx, "real")
	if len(rows) != 1 {
		t.Fatalf("after merge target has %d connections, want 1", len(rows))
	}
}
