package credreq

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/yogasw/wick/internal/entity"
)

// rot is a reversible stand-in for the master-key crypter: enough to prove
// the stored column is not the plaintext.
type rot struct{}

func (rot) EncryptMaster(p string) (string, error) { return "sealed:" + reverse(p), nil }
func (rot) DecryptMaster(t string) (string, error) {
	return reverse(strings.TrimPrefix(t, "sealed:")), nil
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

type pubRec struct{ views []View }

func (p *pubRec) Publish(_ context.Context, v View) (map[string]string, error) {
	p.views = append(p.views, v)
	return map[string]string{"slack_ts": "1.2"}, nil
}

func newSvc(t *testing.T) (*Service, *gorm.DB, *pubRec, *[]string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&entity.CredentialRequest{}))
	s := New(db, rot{})
	p := &pubRec{}
	s.AddPublisher(p)
	var woke []string
	s.SetWaker(func(_ context.Context, sid, text string) error {
		woke = append(woke, sid+"|"+text)
		return nil
	})
	return s, db, p, &woke
}

func ask(t *testing.T, s *Service) View {
	t.Helper()
	v, err := s.Ask(context.Background(), AskInput{
		SessionID: "sess-1", Title: "Staging login", Reason: "log in to staging",
		Fields: []Field{{Key: "username", Label: "Username"}, {Key: "password", Label: "Password", Secret: true}},
	})
	require.NoError(t, err)
	return v
}

func TestAskValidates(t *testing.T) {
	s, _, _, _ := newSvc(t)
	ctx := context.Background()
	_, err := s.Ask(ctx, AskInput{SessionID: "s", Title: "x", Fields: []Field{{Key: "Bad Key"}}})
	assert.Error(t, err)
	_, err = s.Ask(ctx, AskInput{SessionID: "s", Title: "x", Fields: []Field{{Key: "a"}, {Key: "a"}}})
	assert.Error(t, err)
	_, err = s.Ask(ctx, AskInput{SessionID: "s", Fields: []Field{{Key: "a"}}})
	assert.Error(t, err, "title required")
}

func TestFillAndResolve(t *testing.T) {
	s, db, pub, woke := newSvc(t)
	ctx := context.Background()
	v := ask(t, s)
	assert.Equal(t, StatusPending, v.Status)
	assert.Equal(t, TTLOnce, v.SuggestTTL)
	require.Len(t, pub.views, 1)

	_, err := s.Fill(ctx, FillInput{ID: v.ID, UserID: "u1", UserName: "Filler", TTL: TTL1h, Values: map[string]string{"username": "dev"}})
	assert.ErrorContains(t, err, "Password is required")

	_, err = s.Fill(ctx, FillInput{ID: v.ID, UserID: "", TTL: TTL1h, Values: map[string]string{"username": "dev", "password": "pw"}})
	assert.Error(t, err, "anonymous fill refused")

	got, err := s.Fill(ctx, FillInput{ID: v.ID, UserID: "u1", UserName: "Filler", TTL: TTL1h, Values: map[string]string{"username": "dev", "password": "s3cret"}})
	require.NoError(t, err)
	assert.Equal(t, StatusFilled, got.Status)
	assert.Equal(t, "Filler", got.FilledByName)

	var row entity.CredentialRequest
	require.NoError(t, db.First(&row, "id = ?", v.ID).Error)
	assert.NotContains(t, row.Values, "s3cret", "values are sealed at rest")
	assert.Contains(t, row.Origin, "slack_ts")

	require.Len(t, *woke, 1)
	msg := (*woke)[0]
	assert.Contains(t, msg, Token(v.ID, "password"))
	assert.NotContains(t, msg, "s3cret", "the agent never gets the value")

	val, err := s.Resolve(ctx, "sess-1", Token(v.ID, "password"))
	require.NoError(t, err)
	assert.Equal(t, "s3cret", val)
	val, err = s.Resolve(ctx, "sess-1", Token(v.ID, "password"))
	require.NoError(t, err, "timed retention reads more than once")
	assert.Equal(t, "s3cret", val)

	_, err = s.Resolve(ctx, "other-session", Token(v.ID, "password"))
	assert.ErrorIs(t, err, ErrWrongScope)

	_, err = s.Fill(ctx, FillInput{ID: v.ID, UserID: "u2", TTL: TTL1h, Values: map[string]string{"username": "x", "password": "y"}})
	assert.ErrorIs(t, err, ErrNotPending, "cannot be overwritten once filled")
}

func TestOnceAndExpiry(t *testing.T) {
	s, db, _, _ := newSvc(t)
	ctx := context.Background()
	v := ask(t, s)
	_, err := s.Fill(ctx, FillInput{ID: v.ID, UserID: "u1", TTL: TTLOnce, Values: map[string]string{"username": "dev", "password": "pw"}})
	require.NoError(t, err)

	_, err = s.Resolve(ctx, "sess-1", Token(v.ID, "username"))
	require.NoError(t, err)
	_, err = s.Resolve(ctx, "sess-1", Token(v.ID, "username"))
	assert.ErrorIs(t, err, ErrExpired, "once means once per field")

	_, err = s.Resolve(ctx, "sess-1", Token(v.ID, "password"))
	require.NoError(t, err)
	var row entity.CredentialRequest
	require.NoError(t, db.First(&row, "id = ?", v.ID).Error)
	assert.Equal(t, StatusExpired, row.Status, "all fields read → wiped")
	assert.Empty(t, row.Values)

	// Timed retention expires.
	v2 := ask(t, s)
	_, err = s.Fill(ctx, FillInput{ID: v2.ID, UserID: "u1", TTL: TTL1h, Values: map[string]string{"username": "dev", "password": "pw"}})
	require.NoError(t, err)
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	_, err = s.Resolve(ctx, "sess-1", Token(v2.ID, "password"))
	assert.ErrorIs(t, err, ErrExpired)
	var row2 entity.CredentialRequest
	require.NoError(t, db.First(&row2, "id = ?", v2.ID).Error)
	assert.Empty(t, row2.Values)
}

func TestSweepAndCancel(t *testing.T) {
	s, db, _, _ := newSvc(t)
	ctx := context.Background()
	v := ask(t, s)
	_, err := s.Cancel(ctx, v.ID, "another-session")
	assert.ErrorIs(t, err, ErrNotFound)
	c, err := s.Cancel(ctx, v.ID, "sess-1")
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, c.Status)

	v2 := ask(t, s)
	_, err = s.Fill(ctx, FillInput{ID: v2.ID, UserID: "u1", TTL: TTL24h, Values: map[string]string{"username": "dev", "password": "pw"}})
	require.NoError(t, err)
	s.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	n, err := s.Sweep(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var row entity.CredentialRequest
	require.NoError(t, db.First(&row, "id = ?", v2.ID).Error)
	assert.Equal(t, StatusExpired, row.Status)
	assert.Empty(t, row.Values)
}

func TestParseToken(t *testing.T) {
	id := "0b1c2d3e-0000-4000-8000-000000000001"
	gid, key, ok := ParseToken(Token(id, "password"))
	assert.True(t, ok)
	assert.Equal(t, id, gid)
	assert.Equal(t, "password", key)
	_, _, ok = ParseToken("wick_enc_abc")
	assert.False(t, ok)
}

func TestUseKeepsItAlive(t *testing.T) {
	s, _, _, _ := newSvc(t)
	ctx := context.Background()
	v := ask(t, s)
	base := time.Now()
	s.now = func() time.Time { return base }
	_, err := s.Fill(ctx, FillInput{ID: v.ID, UserID: "u1", TTL: TTL1h, Values: map[string]string{"username": "dev", "password": "pw"}})
	require.NoError(t, err)
	// Read every 50 minutes for 5 hours: never idle for an hour, never wiped.
	for i := 1; i <= 6; i++ {
		at := base.Add(time.Duration(i*50) * time.Minute)
		s.now = func() time.Time { return at }
		_, err := s.Resolve(ctx, "sess-1", Token(v.ID, "password"))
		require.NoError(t, err, "read %d", i)
	}
	// Then idle for over an hour: gone.
	last := base.Add(300*time.Minute + 61*time.Minute)
	s.now = func() time.Time { return last }
	_, err = s.Resolve(ctx, "sess-1", Token(v.ID, "password"))
	assert.ErrorIs(t, err, ErrExpired)
}

func TestSaveDirect(t *testing.T) {
	s, _, pub, woke := newSvc(t)
	ctx := context.Background()
	_, err := s.Save(ctx, SaveInput{SessionID: "sess-1", UserID: "u1", TTL: TTL24h, Entries: []Entry{{Key: "", Value: "x"}}})
	assert.Error(t, err, "value without key")
	_, err = s.Save(ctx, SaveInput{SessionID: "sess-1", UserID: "u1", TTL: TTL24h, Entries: []Entry{{}}})
	assert.Error(t, err, "nothing to save")

	v, err := s.Save(ctx, SaveInput{SessionID: "sess-1", UserID: "u1", UserName: "Dev", TTL: TTL24h, Entries: []Entry{
		{Key: "Password", Value: "hunter2", Secret: true},
		{Key: "base_url", Value: "https://stag.example.com", Secret: false},
		{},
	}})
	require.NoError(t, err)
	assert.Equal(t, StatusFilled, v.Status)
	require.Len(t, *woke, 1)
	msg := (*woke)[0]
	assert.Contains(t, msg, Token(v.ID, "password"))
	assert.NotContains(t, msg, "hunter2", "secret stays a token")
	assert.Contains(t, msg, "https://stag.example.com", "non-secret is handed over as is")
	assert.Len(t, pub.views, 1, "no pending card for a direct save, only the filled one")

	got, err := s.Resolve(ctx, "sess-1", Token(v.ID, "password"))
	require.NoError(t, err)
	assert.Equal(t, "hunter2", got)
}
