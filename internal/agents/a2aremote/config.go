// Package a2aremote makes wick an A2A client: an agent of another system,
// reached over the A2A protocol, joins a Team roster as an agent of kind
// "a2a-remote". It has no local process — each turn is one call to the
// remote endpoint, and its events are written as the stream-json lines the
// rest of the pool already reads (see process.go).
//
// The remote's settings live on an agent_channels row (type RowType, one
// per agent, named after the agent id) the way an agent's A2A server
// connection does. The auth secret is stored encrypted with the configs
// codec, like a connector's config, and never leaves the package in a
// response or a log line.
package a2aremote

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"

	"github.com/yogasw/wick/internal/entity"
)

// Kind is the AgentPersona.Kind of a remote agent.
const Kind = "a2a-remote"

// ProviderKey is the "type/name" a remote agent's sessions run under. No
// provider instance carries it: the pool's factory hands such a session to
// this package's Spawner (see pool.ClaudeFactory.RemoteSpawnerLoader).
const ProviderKey = "a2a-remote/a2a-remote"

// RowType is the agent_channels.type of a remote agent's settings row.
const RowType = "a2a-remote"

// Origin marks a session that talks to a remote agent.
const Origin = "a2a-remote"

// ProjectTag marks the project made to hold a remote agent's transcripts:
// it has no persona or provider to edit, so project pickers leave it out.
const ProjectTag = "wick:a2a-remote"

// Defaults and bounds of the per-call limits.
const (
	DefaultTimeoutSec       = 120
	MaxTimeoutSec           = 900
	DefaultMaxResponseBytes = 2 << 20
	MaxMaxResponseBytes     = 32 << 20
	minMaxResponseBytes     = 1 << 10
)

// Usage values: who may hand the agent a turn.
const (
	// UsageOnlyMe — the owner alone, from the app. Mentions from the
	// owner's other agents are refused.
	UsageOnlyMe = "only_me"
	// UsageMeAndAgents — the owner, plus the owner's agents via @mention.
	UsageMeAndAgents = "me_and_my_agents"
	// UsageByMention — the agent's mention policy decides (Settings ›
	// Mention); a remote agent saved since the two settings merged, or
	// one whose old usage was carried into the policy.
	UsageByMention = "mention"
)

// Auth types.
const (
	AuthNone   = "none"
	AuthBearer = "bearer"
	AuthAPIKey = "api_key"
)

// DefaultAPIKeyHeader is the header an api_key secret travels in when
// none is named.
const DefaultAPIKeyHeader = "X-API-Key"

// Codec encrypts and decrypts secrets. *configs.Service implements it —
// the same codec a connector's secret config goes through.
type Codec interface {
	EncryptSecret(plain string) (string, error)
	DecryptSecret(token string) (string, error)
}

// Auth is how wick authenticates to the remote. Secret is the ENCRYPTED
// token; the plaintext is only ever produced by Plain, for one call.
type Auth struct {
	Type   string `json:"type"`
	Header string `json:"header,omitempty"`
	Secret string `json:"secret,omitempty"`
}

// Set reports whether a secret is stored.
func (a Auth) Set() bool { return a.Type != "" && a.Type != AuthNone && a.Secret != "" }

// PlainAuth is an auth with its secret in the clear, for one request. It
// is never marshalled: no JSON tags, and String hides the secret.
type PlainAuth struct {
	Type   string
	Header string
	Secret string
}

// String keeps the secret out of any %v / log line.
func (a PlainAuth) String() string {
	if a.Secret == "" {
		return a.Type
	}
	return a.Type + ":***"
}

// NormalizeAuth validates a typed auth and fills the api_key header.
func NormalizeAuth(a PlainAuth) (PlainAuth, error) {
	a.Type = strings.ToLower(strings.TrimSpace(a.Type))
	a.Header = strings.TrimSpace(a.Header)
	a.Secret = strings.TrimSpace(a.Secret)
	switch a.Type {
	case "", AuthNone:
		return PlainAuth{Type: AuthNone}, nil
	case AuthBearer:
		a.Header = ""
	case AuthAPIKey:
		if a.Header == "" {
			a.Header = DefaultAPIKeyHeader
		}
		if !validHeaderName(a.Header) {
			return a, fmt.Errorf("invalid header name %q", a.Header)
		}
	default:
		return a, fmt.Errorf("auth type must be none, bearer or api_key")
	}
	if a.Secret == "" {
		return a, fmt.Errorf("%s auth needs a secret", a.Type)
	}
	if strings.ContainsAny(a.Secret, "\r\n") {
		return a, errors.New("secret must be one line")
	}
	return a, nil
}

func validHeaderName(h string) bool {
	if h == "" || len(h) > 64 {
		return false
	}
	for _, r := range h {
		if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// Seal encrypts a.
func Seal(codec Codec, a PlainAuth) (Auth, error) {
	if a.Type == "" || a.Type == AuthNone {
		return Auth{Type: AuthNone}, nil
	}
	if codec == nil {
		return Auth{}, errors.New("secret storage is not available")
	}
	enc, err := codec.EncryptSecret(a.Secret)
	if err != nil {
		return Auth{}, fmt.Errorf("encrypt secret: %w", err)
	}
	return Auth{Type: a.Type, Header: a.Header, Secret: enc}, nil
}

// Plain decrypts a for one call.
func (a Auth) Plain(codec Codec) (PlainAuth, error) {
	if !a.Set() {
		return PlainAuth{Type: AuthNone}, nil
	}
	if codec == nil {
		return PlainAuth{}, errors.New("secret storage is not available")
	}
	plain, err := codec.DecryptSecret(a.Secret)
	if err != nil {
		// The codec's own error may quote the token; say only what failed.
		return PlainAuth{}, errors.New("decrypt secret failed")
	}
	return PlainAuth{Type: a.Type, Header: a.Header, Secret: plain}, nil
}

// Skill is one card skill as the UI shows it.
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

// Card is the snapshot of the remote's agent card the UI renders.
type Card struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Version     string  `json:"version"`
	Streaming   bool    `json:"streaming"`
	IconURL     string  `json:"icon_url,omitempty"`
	Provider    string  `json:"provider,omitempty"`
	Skills      []Skill `json:"skills"`
	// Endpoint is the URL turns are sent to, the card's preferred
	// interface.
	Endpoint string `json:"endpoint"`
	// Transport is that interface's protocol (JSONRPC, HTTP+JSON).
	Transport string `json:"transport"`
}

// SnapshotOf is the UI snapshot of card.
func SnapshotOf(card *a2a.AgentCard) Card {
	c := Card{
		Name: strings.TrimSpace(card.Name), Description: strings.TrimSpace(card.Description),
		Version: card.Version, Streaming: card.Capabilities.Streaming, IconURL: card.IconURL,
		Skills: []Skill{},
	}
	if card.Provider != nil {
		c.Provider = card.Provider.Org
	}
	for _, s := range card.Skills {
		c.Skills = append(c.Skills, Skill{ID: s.ID, Name: s.Name, Description: s.Description, Examples: s.Examples})
	}
	if len(card.SupportedInterfaces) > 0 && card.SupportedInterfaces[0] != nil {
		c.Endpoint = card.SupportedInterfaces[0].URL
		c.Transport = string(card.SupportedInterfaces[0].ProtocolBinding)
	}
	return c
}

// Config is one remote agent's settings.
type Config struct {
	AgentID     string `json:"-"`
	OwnerUserID string `json:"-"`
	// CardURL is where the card was fetched from; refresh reads it again.
	CardURL string `json:"card_url"`
	Card    Card   `json:"card"`
	// CardJSON is the card as fetched: the client is built from it.
	CardJSON         json.RawMessage `json:"card_json"`
	Auth             Auth            `json:"auth"`
	TimeoutSec       int             `json:"timeout_sec"`
	MaxResponseBytes int64           `json:"max_response_bytes"`
	Usage            string          `json:"usage"`
	RefreshedAt      time.Time       `json:"refreshed_at"`
}

// Timeout is the per-call timeout, default applied.
func (c Config) Timeout() time.Duration {
	if c.TimeoutSec <= 0 {
		return DefaultTimeoutSec * time.Second
	}
	return time.Duration(c.TimeoutSec) * time.Second
}

// MaxBytes is the per-call response cap, default applied.
func (c Config) MaxBytes() int64 {
	if c.MaxResponseBytes <= 0 {
		return DefaultMaxResponseBytes
	}
	return c.MaxResponseBytes
}

// EffectiveUsage is Usage with the default applied.
func (c Config) EffectiveUsage() string {
	switch c.Usage {
	case UsageMeAndAgents, UsageByMention:
		return c.Usage
	}
	return UsageOnlyMe
}

// ParsedCard is CardJSON decoded.
func (c Config) ParsedCard() (*a2a.AgentCard, error) {
	if len(c.CardJSON) == 0 {
		return nil, errors.New("no agent card stored: refresh the card")
	}
	return agentcard.DefaultCardParser(c.CardJSON)
}

// ValidateLimits checks typed limits; zero keeps the default.
func ValidateLimits(timeoutSec int, maxBytes int64, usage string) error {
	if timeoutSec < 0 || timeoutSec > MaxTimeoutSec {
		return fmt.Errorf("timeout_sec must be 1..%d", MaxTimeoutSec)
	}
	if maxBytes != 0 && (maxBytes < minMaxResponseBytes || maxBytes > MaxMaxResponseBytes) {
		return fmt.Errorf("max_response_bytes must be %d..%d", minMaxResponseBytes, MaxMaxResponseBytes)
	}
	switch usage {
	case "", UsageOnlyMe, UsageMeAndAgents, UsageByMention:
	default:
		return fmt.Errorf("usage must be %s, %s or %s", UsageOnlyMe, UsageMeAndAgents, UsageByMention)
	}
	return nil
}

// Store keeps remote settings on agent_channels rows.
type Store struct{ db *gorm.DB }

// NewStore wraps db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// RowID is the agent_channels primary key of agentID's settings.
func RowID(agentID string) string { return RowType + ":" + agentID }

// Load returns agentID's settings; ok=false when it has none.
func (s *Store) Load(agentID string) (Config, bool, error) {
	var rows []entity.AgentChannel
	if err := s.db.Where("type = ? AND name = ?", RowType, agentID).Limit(1).Find(&rows).Error; err != nil {
		return Config{}, false, err
	}
	if len(rows) == 0 {
		return Config{}, false, nil
	}
	var c Config
	if err := json.Unmarshal([]byte(rows[0].Config), &c); err != nil {
		return Config{}, false, err
	}
	c.AgentID = agentID
	if rows[0].UserID != nil {
		c.OwnerUserID = *rows[0].UserID
	}
	return c, true, nil
}

// Save upserts c's row.
func (s *Store) Save(c Config) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	now := time.Now()
	res := s.db.Model(&entity.AgentChannel{}).Where("type = ? AND name = ?", RowType, c.AgentID).
		Updates(map[string]any{"config": string(data), "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	owner := c.OwnerUserID
	return s.db.Create(&entity.AgentChannel{
		ID: RowID(c.AgentID), Type: RowType, Name: c.AgentID, UserID: &owner,
		Enabled: true, Config: string(data), CreatedAt: now, UpdatedAt: now,
	}).Error
}

// Delete drops agentID's settings, if any.
func (s *Store) Delete(agentID string) error {
	return s.db.Where("type = ? AND name = ?", RowType, agentID).Delete(&entity.AgentChannel{}).Error
}
