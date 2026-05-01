package oauth

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Repo wraps gorm with the queries the OAuth handler + token validator
// need. Kept narrow on purpose — this package is auth-critical, easier
// to reason about when the DB surface is small.
type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// ── Clients (DCR) ────────────────────────────────────────────────────

func (r *Repo) CreateClient(ctx context.Context, c *entity.OAuthClient) error {
	return r.db.WithContext(ctx).Create(c).Error
}

func (r *Repo) GetClient(ctx context.Context, clientID string) (*entity.OAuthClient, error) {
	var c entity.OAuthClient
	err := r.db.WithContext(ctx).Where("client_id = ?", clientID).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ── Authorization codes ──────────────────────────────────────────────

func (r *Repo) CreateAuthCode(ctx context.Context, c *entity.OAuthAuthorizationCode) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// ConsumeAuthCode looks up an unused, unexpired code and marks it
// used in a single transaction so a replay can't race. Returns the
// row as it was right before flipping Used.
func (r *Repo) ConsumeAuthCode(ctx context.Context, code string) (*entity.OAuthAuthorizationCode, error) {
	var row entity.OAuthAuthorizationCode
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("code = ? AND used = ? AND expires_at > ?", code, false, time.Now()).
			First(&row).Error; err != nil {
			return err
		}
		return tx.Model(&entity.OAuthAuthorizationCode{}).
			Where("id = ?", row.ID).
			Update("used", true).Error
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ── Tokens ───────────────────────────────────────────────────────────

func (r *Repo) CreateToken(ctx context.Context, t *entity.OAuthToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *Repo) FindActiveTokenByHash(ctx context.Context, hash string) (*entity.OAuthToken, error) {
	var t entity.OAuthToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", hash, time.Now()).
		First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// FindAnyTokenByHash returns the row regardless of revocation/expiry.
// Used by /token refresh to detect replay of a rotated refresh token
// (and revoke the chain).
func (r *Repo) FindAnyTokenByHash(ctx context.Context, hash string) (*entity.OAuthToken, error) {
	var t entity.OAuthToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", hash).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// RevokeChain stamps RevokedAt on every token whose ParentTokenID
// transitively reaches the given root. Called when refresh-token
// reuse is detected so a leaked refresh + its descendants are all
// killed at once. Best-effort — small chains, no recursion limit.
func (r *Repo) RevokeChain(ctx context.Context, rootID string) error {
	now := time.Now()
	frontier := []string{rootID}
	visited := map[string]bool{}
	for len(frontier) > 0 {
		next := frontier[:0]
		for _, id := range frontier {
			if visited[id] {
				continue
			}
			visited[id] = true
			if err := r.db.WithContext(ctx).Model(&entity.OAuthToken{}).
				Where("id = ? AND revoked_at IS NULL", id).
				Update("revoked_at", &now).Error; err != nil {
				return err
			}
			var children []entity.OAuthToken
			if err := r.db.WithContext(ctx).
				Where("parent_token_id = ?", id).
				Find(&children).Error; err != nil {
				return err
			}
			for _, c := range children {
				next = append(next, c.ID)
			}
		}
		frontier = next
	}
	return nil
}

// MarkUsed stamps LastUsedAt on a token. Best-effort observability
// hook the bearer middleware fires after a successful auth.
func (r *Repo) MarkUsed(ctx context.Context, id string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.OAuthToken{}).
		Where("id = ?", id).
		Update("last_used_at", &now).Error
}

// Revoke stamps RevokedAt on a single token row. Used when rotating
// refresh tokens: the old refresh is marked revoked the moment its
// successor is minted.
func (r *Repo) Revoke(ctx context.Context, id string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.OAuthToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", &now).Error
}

// ── Grants (per-user dashboard) ──────────────────────────────────────

// Grant is the aggregated view of one app the user has currently
// authorized — collapsing the underlying token rows down to the app
// metadata + lifetime markers the dashboard cares about.
type Grant struct {
	ClientID   string
	ClientName string
	GrantedAt  time.Time  // earliest CreatedAt across the active tokens
	LastUsedAt *time.Time // most recent LastUsedAt, nil if never
	TokenCount int        // active access + refresh tokens
}

// ListGrantsByUser returns one Grant per app the user has currently
// authorized. "Currently" = at least one non-revoked, non-expired
// token row of either kind (refresh keeps the grant alive even after
// the access expires).
//
// Ordered newest-grant first so the dashboard reads chronologically.
//
// Implementation notes: SQLite returns MIN/MAX of TIMESTAMP columns
// as raw strings (the driver only auto-parses real columns, not
// aggregate expressions), so we scan those into strings and parse
// them in Go. parseAggregateTime accepts both SQLite's "YYYY-MM-DD
// HH:MM:SS[.fff]" and Postgres's RFC3339 — the same code works on
// either backend without runtime feature detection.
func (r *Repo) ListGrantsByUser(ctx context.Context, userID string) ([]Grant, error) {
	type row struct {
		ClientID   string
		ClientName string
		GrantedAt  string  // raw timestamp string (parsed below)
		LastUsedAt *string // null when never used
		TokenCount int
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("oauth_tokens AS t").
		Select(`t.client_id            AS client_id,
		         c.name                AS client_name,
		         MIN(t.created_at)     AS granted_at,
		         MAX(t.last_used_at)   AS last_used_at,
		         COUNT(*)              AS token_count`).
		Joins("JOIN oauth_clients c ON c.client_id = t.client_id").
		Where("t.user_id = ? AND t.revoked_at IS NULL AND t.expires_at > ?", userID, time.Now()).
		Group("t.client_id, c.name").
		Order("MIN(t.created_at) DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Grant, len(rows))
	for i, x := range rows {
		grantedAt, _ := parseAggregateTime(x.GrantedAt)
		var lastUsed *time.Time
		if x.LastUsedAt != nil {
			if t, ok := parseAggregateTime(*x.LastUsedAt); ok {
				lastUsed = &t
			}
		}
		out[i] = Grant{
			ClientID:   x.ClientID,
			ClientName: x.ClientName,
			GrantedAt:  grantedAt,
			LastUsedAt: lastUsed,
			TokenCount: x.TokenCount,
		}
	}
	return out, nil
}

// parseAggregateTime tries the layouts SQLite and Postgres use when
// returning aggregate timestamp expressions. Returns the zero time
// + false when none match — the caller treats that as "unknown" and
// renders accordingly.
func parseAggregateTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// RevokeAllForUserClient revokes every active token (access + refresh)
// the user holds for the given client. Used by the profile
// "Disconnect" button — once the user clicks it, the client must
// re-run the OAuth dance to regain access. Idempotent.
func (r *Repo) RevokeAllForUserClient(ctx context.Context, userID, clientID string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.OAuthToken{}).
		Where("user_id = ? AND client_id = ? AND revoked_at IS NULL", userID, clientID).
		Update("revoked_at", &now).Error
}
