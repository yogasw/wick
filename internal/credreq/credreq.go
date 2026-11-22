// Package credreq lets an agent ask a person for credentials without the
// values ever passing through the chat.
//
// The agent files a request (wick_ask_credentials) naming the fields it
// needs and ends its turn. A card appears where the session lives (a Slack
// thread opens a modal, the web shows a form); a wick user fills it and
// picks how long they may sit unused: once, or 1 hour up to 7 days (every use resets the clock). The
// values are stored encrypted. The session is woken with reference tokens
// (wick_cred_<request>.<field>) — never the values. A script in the session
// resolves a token at runtime (support-tools agent secret <token>), so the
// plaintext lives only inside that process.
package credreq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Statuses.
const (
	StatusPending   = "pending"
	StatusFilled    = "filled"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"
)

// Retention choices. Timed choices are an IDLE window, not a lifetime:
// every read pushes the expiry out again, so credentials a script keeps
// using are never deleted under it; ones nobody touches for the window are
// wiped. TTLOnce lets every field be read exactly one time.
const (
	TTLOnce = "once"
	TTL1h   = "1h"
	TTL24h  = "24h"
	TTL3d   = "3d"
	TTL7d   = "7d"
)

// TTLs lists the choices in display order.
var TTLs = []string{TTLOnce, TTL1h, TTL24h, TTL3d, TTL7d}

// TTLLabel is the human label of a retention choice.
func TTLLabel(ttl string) string {
	switch ttl {
	case TTLOnce:
		return "Once (single use)"
	case TTL1h:
		return "Until unused for 1 hour"
	case TTL24h:
		return "Until unused for 1 day"
	case TTL3d:
		return "Until unused for 3 days"
	case TTL7d:
		return "Until unused for 7 days"
	}
	return ttl
}

// ttlDuration is the idle window. "once" still expires after an hour
// unread so a token nobody uses does not linger.
func ttlDuration(ttl string) (time.Duration, bool) {
	switch ttl {
	case TTLOnce, TTL1h:
		return time.Hour, true
	case TTL24h:
		return 24 * time.Hour, true
	case TTL3d:
		return 72 * time.Hour, true
	case TTL7d:
		return 7 * 24 * time.Hour, true
	}
	return 0, false
}

// PendingTTL is how long a request waits to be filled.
const PendingTTL = 24 * time.Hour

// TokenPrefix starts every reference token.
const TokenPrefix = "wick_cred_"

var (
	keyRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	tokenRe = regexp.MustCompile(`^wick_cred_([0-9a-f-]{36})\.([a-z][a-z0-9_]{0,39})$`)
)

// Token is the reference token for one field of a request.
func Token(requestID, key string) string { return TokenPrefix + requestID + "." + key }

// ParseToken splits a reference token.
func ParseToken(tok string) (requestID, key string, ok bool) {
	m := tokenRe.FindStringSubmatch(strings.TrimSpace(tok))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// Field is one value the agent asks for.
type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// View is a request as every surface draws it. It never carries values.
type View struct {
	ID           string            `json:"id"`
	SessionID    string            `json:"session_id"`
	AgentName    string            `json:"agent_name"`
	Title        string            `json:"title"`
	Reason       string            `json:"reason"`
	Fields       []Field           `json:"fields"`
	SuggestTTL   string            `json:"suggest_ttl"`
	Status       string            `json:"status"`
	TTL          string            `json:"ttl,omitempty"`
	FilledByName string            `json:"filled_by_name,omitempty"`
	FilledAt     *time.Time        `json:"filled_at,omitempty"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
	Origin       map[string]string `json:"-"`
}

// Publisher draws a request somewhere (Slack card, web turn). It returns
// origin fields to remember (e.g. the Slack message ts) or nil.
type Publisher interface {
	Publish(ctx context.Context, v View) (map[string]string, error)
}

// Crypter encrypts values at rest.
type Crypter interface {
	EncryptMaster(plain string) (string, error)
	DecryptMaster(token string) (string, error)
}

// Service owns credential requests.
type Service struct {
	db         *gorm.DB
	crypt      Crypter
	publishers []Publisher
	wake       func(ctx context.Context, sessionID, text string) error
	now        func() time.Time
}

// New builds the service. crypt must not be nil.
func New(db *gorm.DB, crypt Crypter) *Service {
	return &Service{db: db, crypt: crypt, now: time.Now}
}

// AddPublisher registers a surface that draws cards.
func (s *Service) AddPublisher(p Publisher) { s.publishers = append(s.publishers, p) }

// SetWaker wires how a filled request wakes its session.
func (s *Service) SetWaker(fn func(ctx context.Context, sessionID, text string) error) { s.wake = fn }

// Errors callers map to responses.
var (
	ErrNotFound   = errors.New("credential request not found")
	ErrNotPending = errors.New("this credential request is no longer waiting to be filled")
	ErrExpired    = errors.New("these credentials have expired or were already used; ask for them again")
	ErrWrongScope = errors.New("this token belongs to another session")
)

// AskInput is what the agent sends.
type AskInput struct {
	SessionID       string
	RequesterUserID string
	AgentName       string
	Title           string
	Reason          string
	Fields          []Field
	SuggestTTL      string
}

// Ask files a request and draws its card.
func (s *Service) Ask(ctx context.Context, in AskInput) (View, error) {
	return s.ask(ctx, in, true)
}

func (s *Service) ask(ctx context.Context, in AskInput, draw bool) (View, error) {
	if in.SessionID == "" {
		return View{}, errors.New("credential requests need a session")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return View{}, errors.New("title is required (what the credentials are for)")
	}
	if len(in.Fields) == 0 || len(in.Fields) > 8 {
		return View{}, errors.New("ask for 1 to 8 fields")
	}
	seen := map[string]bool{}
	fields := make([]Field, 0, len(in.Fields))
	for _, f := range in.Fields {
		f.Key = strings.TrimSpace(strings.ToLower(f.Key))
		if !keyRe.MatchString(f.Key) {
			return View{}, fmt.Errorf("field key %q must be lower_snake_case (a-z, 0-9, _), up to 40 chars", f.Key)
		}
		if seen[f.Key] {
			return View{}, fmt.Errorf("field key %q is repeated", f.Key)
		}
		seen[f.Key] = true
		f.Label = clip(strings.TrimSpace(f.Label), 60)
		if f.Label == "" {
			f.Label = f.Key
		}
		f.Hint = clip(strings.TrimSpace(f.Hint), 120)
		fields = append(fields, f)
	}
	ttl := in.SuggestTTL
	if _, ok := ttlDuration(ttl); !ok {
		ttl = TTLOnce
	}
	raw, _ := json.Marshal(fields)
	now := s.now()
	row := entity.CredentialRequest{
		ID: uuid.NewString(), SessionID: in.SessionID, RequesterUserID: in.RequesterUserID,
		AgentName: clip(in.AgentName, 80), Title: clip(title, 120), Reason: clip(strings.TrimSpace(in.Reason), 500),
		Fields: string(raw), SuggestTTL: ttl, Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return View{}, err
	}
	v := viewOf(&row)
	if draw {
		s.publish(ctx, &row, v)
	}
	return v, nil
}

// Get returns a request's view.
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	row, err := s.load(ctx, id)
	if err != nil {
		return View{}, err
	}
	return viewOf(row), nil
}

// FillInput is what the person submits.
type FillInput struct {
	ID         string
	UserID     string
	UserName   string
	Values     map[string]string
	TTL        string
	SessionCut func(sessionID string) bool // optional: true when the session is gone
}

// Fill stores the values and wakes the session with reference tokens.
func (s *Service) Fill(ctx context.Context, in FillInput) (View, error) {
	if in.UserID == "" {
		return View{}, errors.New("only a signed-in wick user can fill credentials")
	}
	dur, ok := ttlDuration(in.TTL)
	if !ok {
		return View{}, fmt.Errorf("pick how long to keep them: %s", strings.Join(TTLs, ", "))
	}
	row, err := s.load(ctx, in.ID)
	if err != nil {
		return View{}, err
	}
	s.expireIfDue(ctx, row)
	if row.Status != StatusPending {
		return viewOf(row), ErrNotPending
	}
	fields := fieldsOf(row)
	clean := map[string]string{}
	for _, f := range fields {
		v := in.Values[f.Key]
		if strings.TrimSpace(v) == "" {
			if !f.Optional {
				return viewOf(row), fmt.Errorf("%s is required", f.Label)
			}
			continue
		}
		if len(v) > 4096 {
			return viewOf(row), fmt.Errorf("%s is too long", f.Label)
		}
		clean[f.Key] = v
	}
	raw, _ := json.Marshal(clean)
	sealed, err := s.crypt.EncryptMaster(string(raw))
	if err != nil {
		return viewOf(row), fmt.Errorf("encrypt: %w", err)
	}
	now := s.now()
	exp := now.Add(dur)
	res := s.db.WithContext(ctx).Model(&entity.CredentialRequest{}).
		Where("id = ? AND status = ?", row.ID, StatusPending).
		Updates(map[string]any{
			"status": StatusFilled, "values": sealed, "ttl": in.TTL, "used": "[]",
			"filled_by": in.UserID, "filled_by_name": clip(in.UserName, 80),
			"filled_at": now, "expires_at": exp, "updated_at": now,
		})
	if res.Error != nil {
		return viewOf(row), res.Error
	}
	if res.RowsAffected == 0 {
		return viewOf(row), ErrNotPending // filled by someone else a moment ago
	}
	row, err = s.load(ctx, in.ID)
	if err != nil {
		return View{}, err
	}
	v := viewOf(row)
	s.publish(ctx, row, v)
	if s.wake != nil {
		_ = s.wake(ctx, row.SessionID, filledMessage(v, clean, fields))
	}
	return v, nil
}

// filledMessage is what the agent reads: tokens and how to use them, never
// the values. Only keys that were actually filled get a token.
func filledMessage(v View, filled map[string]string, fields []Field) string {
	secret := map[string]bool{}
	for _, f := range fields {
		secret[f.Key] = f.Secret
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[credentials] %q was filled by %s (kept: %s).\n", v.Title, v.FilledByName, TTLLabel(v.TTL))
	keys := make([]string, 0, len(filled))
	for k := range filled {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	hasSecret := false
	for _, k := range keys {
		if secret[k] {
			hasSecret = true
			fmt.Fprintf(&b, "- %s (secret): %s\n", k, Token(v.ID, k))
		} else {
			// Marked not secret by the person: the value itself, so the
			// agent does not have to ask for it in chat.
			fmt.Fprintf(&b, "- %s: %s\n", k, filled[k])
		}
	}
	if hasSecret {
		b.WriteString("Secret entries are reference tokens, not the values. Resolve one only inside a script at the moment it is needed: " +
			"`support-tools agent secret <token>` prints the value to stdout (needs WICK_CLI_TOKEN from wick_cli_token). " +
			"Never echo, log or paste a resolved value, and never ask the person for it in chat.")
		if v.TTL == TTLOnce {
			b.WriteString(" They were given for ONE use: each token resolves a single time.")
		}
	}
	return b.String()
}

// Entry is one row a person types into the session-secret form.
type Entry struct {
	Key    string
	Value  string
	Secret bool
}

// SaveInput is a person handing values to a session on their own (the
// Slack shortcut or slash command): no agent asked, nothing pending.
type SaveInput struct {
	SessionID string
	UserID    string
	UserName  string
	Title     string
	Entries   []Entry
	TTL       string
}

// Save stores values a person typed for a session and wakes it. Rows with
// an empty key and value are skipped.
func (s *Service) Save(ctx context.Context, in SaveInput) (View, error) {
	var fields []Field
	values := map[string]string{}
	for _, e := range in.Entries {
		k := strings.TrimSpace(strings.ToLower(e.Key))
		if k == "" && strings.TrimSpace(e.Value) == "" {
			continue
		}
		if k == "" {
			return View{}, errors.New("every value needs a key")
		}
		if strings.TrimSpace(e.Value) == "" {
			return View{}, fmt.Errorf("%s has no value", k)
		}
		fields = append(fields, Field{Key: k, Label: k, Secret: e.Secret})
		values[k] = e.Value
	}
	if len(fields) == 0 {
		return View{}, errors.New("add at least one key and value")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Session secrets"
	}
	v, err := s.ask(ctx, AskInput{SessionID: in.SessionID, RequesterUserID: in.UserID, Title: title, Fields: fields, SuggestTTL: in.TTL}, false)
	if err != nil {
		return View{}, err
	}
	return s.Fill(ctx, FillInput{ID: v.ID, UserID: in.UserID, UserName: in.UserName, Values: values, TTL: in.TTL})
}

// Resolve returns one field's plaintext for a script running in sessionID.
func (s *Service) Resolve(ctx context.Context, sessionID, token string) (string, error) {
	id, key, ok := ParseToken(token)
	if !ok {
		return "", errors.New("not a credential token (wick_cred_<id>.<field>)")
	}
	row, err := s.load(ctx, id)
	if err != nil {
		return "", err
	}
	if row.SessionID != sessionID {
		return "", ErrWrongScope
	}
	s.expireIfDue(ctx, row)
	if row.Status != StatusFilled || row.Values == "" {
		return "", ErrExpired
	}
	var used []string
	_ = json.Unmarshal([]byte(row.Used), &used)
	if row.TTL == TTLOnce {
		for _, u := range used {
			if u == key {
				return "", ErrExpired
			}
		}
	}
	plain, err := s.crypt.DecryptMaster(row.Values)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	var vals map[string]string
	if err := json.Unmarshal([]byte(plain), &vals); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	val, ok := vals[key]
	if !ok {
		return "", fmt.Errorf("field %q was not filled", key)
	}
	if row.TTL != TTLOnce {
		// Sliding window: a read keeps the values alive.
		if dur, ok := ttlDuration(row.TTL); ok {
			exp := s.now().Add(dur)
			_ = s.db.WithContext(ctx).Model(&entity.CredentialRequest{}).Where("id = ? AND status = ?", row.ID, StatusFilled).
				Updates(map[string]any{"expires_at": exp, "updated_at": s.now()}).Error
		}
	}
	if row.TTL == TTLOnce {
		used = append(used, key)
		upd := map[string]any{"used": mustJSON(used), "updated_at": s.now()}
		if len(used) >= len(vals) {
			upd["values"] = ""
			upd["status"] = StatusExpired
		}
		// Guarded on the old Used so two concurrent reads cannot both pass.
		res := s.db.WithContext(ctx).Model(&entity.CredentialRequest{}).
			Where("id = ? AND used = ?", row.ID, row.Used).Updates(upd)
		if res.Error != nil {
			return "", res.Error
		}
		if res.RowsAffected == 0 {
			return "", ErrExpired
		}
	}
	return val, nil
}

// Cancel withdraws a request and wipes any values.
func (s *Service) Cancel(ctx context.Context, id, sessionID string) (View, error) {
	row, err := s.load(ctx, id)
	if err != nil {
		return View{}, err
	}
	if sessionID != "" && row.SessionID != sessionID {
		return View{}, ErrNotFound
	}
	if row.Status == StatusCancelled || row.Status == StatusExpired {
		return viewOf(row), nil
	}
	if err := s.db.WithContext(ctx).Model(&entity.CredentialRequest{}).Where("id = ?", id).
		Updates(map[string]any{"status": StatusCancelled, "values": "", "updated_at": s.now()}).Error; err != nil {
		return View{}, err
	}
	row, _ = s.load(ctx, id)
	v := viewOf(row)
	s.publish(ctx, row, v)
	return v, nil
}

// Sweep expires due requests (filled past their TTL, pending past
// PendingTTL) and wipes their values. Returns how many changed.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	now := s.now()
	var rows []entity.CredentialRequest
	if err := s.db.WithContext(ctx).
		Where("(status = ? AND expires_at < ?) OR (status = ? AND created_at < ?)",
			StatusFilled, now, StatusPending, now.Add(-PendingTTL)).
		Find(&rows).Error; err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		if s.expire(ctx, &rows[i]) {
			n++
		}
	}
	return n, nil
}

// ForgetSession wipes every request of a session that ended.
func (s *Service) ForgetSession(ctx context.Context, sessionID string) error {
	return s.db.WithContext(ctx).Model(&entity.CredentialRequest{}).
		Where("session_id = ? AND status IN ?", sessionID, []string{StatusPending, StatusFilled}).
		Updates(map[string]any{"status": StatusExpired, "values": "", "updated_at": s.now()}).Error
}

func (s *Service) expireIfDue(ctx context.Context, row *entity.CredentialRequest) {
	now := s.now()
	due := (row.Status == StatusFilled && row.ExpiresAt != nil && now.After(*row.ExpiresAt)) ||
		(row.Status == StatusPending && now.Sub(row.CreatedAt) > PendingTTL)
	if due {
		s.expire(ctx, row)
	}
}

func (s *Service) expire(ctx context.Context, row *entity.CredentialRequest) bool {
	res := s.db.WithContext(ctx).Model(&entity.CredentialRequest{}).
		Where("id = ? AND status IN ?", row.ID, []string{StatusPending, StatusFilled}).
		Updates(map[string]any{"status": StatusExpired, "values": "", "updated_at": s.now()})
	if res.Error != nil || res.RowsAffected == 0 {
		return false
	}
	row.Status, row.Values = StatusExpired, ""
	s.publish(ctx, row, viewOf(row))
	return true
}

func (s *Service) load(ctx context.Context, id string) (*entity.CredentialRequest, error) {
	var row entity.CredentialRequest
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

// publish draws v on every surface and remembers where it was posted.
func (s *Service) publish(ctx context.Context, row *entity.CredentialRequest, v View) {
	origin := map[string]string{}
	_ = json.Unmarshal([]byte(row.Origin), &origin)
	v.Origin = origin
	changed := false
	for _, p := range s.publishers {
		out, err := p.Publish(ctx, v)
		if err != nil {
			continue
		}
		for k, val := range out {
			if origin[k] != val {
				origin[k] = val
				changed = true
			}
		}
	}
	if changed {
		raw := mustJSON(origin)
		row.Origin = raw
		_ = s.db.WithContext(ctx).Model(&entity.CredentialRequest{}).Where("id = ?", row.ID).Update("origin", raw).Error
	}
}

func viewOf(row *entity.CredentialRequest) View {
	origin := map[string]string{}
	_ = json.Unmarshal([]byte(row.Origin), &origin)
	return View{
		ID: row.ID, SessionID: row.SessionID, AgentName: row.AgentName, Title: row.Title, Reason: row.Reason,
		Fields: fieldsOf(row), SuggestTTL: row.SuggestTTL, Status: row.Status, TTL: row.TTL,
		FilledByName: row.FilledByName, FilledAt: row.FilledAt, ExpiresAt: row.ExpiresAt, Origin: origin,
	}
}

func fieldsOf(row *entity.CredentialRequest) []Field {
	var fs []Field
	_ = json.Unmarshal([]byte(row.Fields), &fs)
	return fs
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
