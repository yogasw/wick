package a2aserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// RowType is the agent_channels.type of a Team agent's A2A connection. One
// row per agent, named after the agent id, keyed "a2a-agent:<agent_id>".
const RowType = "a2a-agent"

// keyPrefix marks a connection API key so it is recognisable in a config
// file or a leaked log, and never mistaken for a wick PAT.
const keyPrefix = "wa2a_"

// RowID is the agent_channels primary key of agentID's connection.
func RowID(agentID string) string { return RowType + ":" + agentID }

// Connection is an agent's A2A server settings. The API key is held only
// as a hash: its plaintext exists once, in the response that created it.
type Connection struct {
	AgentID     string
	OwnerUserID string
	Enabled     bool
	// PublicCard serves the agent card without a Bearer. The JSON-RPC
	// endpoint itself always needs one.
	PublicCard bool
	KeyHash    string
	// KeyHint is the key's last four characters, for telling two keys
	// apart in the UI without storing anything usable.
	KeyHint string
	// AllowedCallers are wick user ids whose Personal Access Token may call
	// the agent besides its owner's. The connection key needs no listing.
	AllowedCallers []string
	RotatedAt      *time.Time
}

// HasKey reports whether the connection has a live API key.
func (c Connection) HasKey() bool { return c.KeyHash != "" }

// connConfig is the agent_channels.config JSON of a connection row.
type connConfig struct {
	PublicCard     bool       `json:"public_card"`
	KeyHash        string     `json:"key_hash,omitempty"`
	KeyHint        string     `json:"key_hint,omitempty"`
	AllowedCallers []string   `json:"allowed_callers,omitempty"`
	RotatedAt      *time.Time `json:"rotated_at,omitempty"`
}

// ConnStore reads connections. *Store implements it; tests use a map.
type ConnStore interface {
	Load(agentID string) (Connection, bool, error)
	Delete(agentID string) error
}

// Store keeps connections on agent_channels rows.
type Store struct{ db *gorm.DB }

// NewStore wraps db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Load returns agentID's connection; ok=false when it has none.
func (s *Store) Load(agentID string) (Connection, bool, error) {
	var rows []entity.AgentChannel
	if err := s.db.Where("type = ? AND name = ?", RowType, agentID).Limit(1).Find(&rows).Error; err != nil {
		return Connection{}, false, err
	}
	if len(rows) == 0 {
		return Connection{}, false, nil
	}
	row := rows[0]
	var cfg connConfig
	if row.Config != "" && row.Config != "{}" {
		if err := json.Unmarshal([]byte(row.Config), &cfg); err != nil {
			return Connection{}, false, err
		}
	}
	c := Connection{
		AgentID: agentID, Enabled: row.Enabled, PublicCard: cfg.PublicCard,
		KeyHash: cfg.KeyHash, KeyHint: cfg.KeyHint, AllowedCallers: cfg.AllowedCallers, RotatedAt: cfg.RotatedAt,
	}
	if row.UserID != nil {
		c.OwnerUserID = *row.UserID
	}
	return c, true, nil
}

// Save upserts c's row.
func (s *Store) Save(c Connection) error {
	data, err := json.Marshal(connConfig{
		PublicCard: c.PublicCard, KeyHash: c.KeyHash, KeyHint: c.KeyHint,
		AllowedCallers: c.AllowedCallers, RotatedAt: c.RotatedAt,
	})
	if err != nil {
		return err
	}
	_, found, err := s.Load(c.AgentID)
	if err != nil {
		return err
	}
	now := time.Now()
	if found {
		return s.db.Model(&entity.AgentChannel{}).Where("type = ? AND name = ?", RowType, c.AgentID).
			Updates(map[string]any{"config": string(data), "enabled": c.Enabled, "updated_at": now}).Error
	}
	owner := c.OwnerUserID
	if err := s.db.Create(&entity.AgentChannel{
		ID: RowID(c.AgentID), Type: RowType, Name: c.AgentID, UserID: &owner,
		Enabled: c.Enabled, Config: string(data), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil || c.Enabled {
		return err
	}
	// The column defaults to true and Create drops a false bool as a zero
	// value, so an off row is written off explicitly.
	return s.db.Model(&entity.AgentChannel{}).Where("type = ? AND name = ?", RowType, c.AgentID).
		Update("enabled", false).Error
}

// Delete drops agentID's connection, if any.
func (s *Store) Delete(agentID string) error {
	return s.db.Where("type = ? AND name = ?", RowType, agentID).Delete(&entity.AgentChannel{}).Error
}

// NewKey mints a connection API key: the plaintext to show once, the hash
// to store and the hint to display.
func NewKey() (plain, hash, hint string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plain = keyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plain, HashKey(plain), plain[len(plain)-4:], nil
}

// HashKey is the stored form of a key. The key carries 256 random bits, so
// a plain SHA-256 is enough — there is nothing to brute-force.
func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// VerifyKey reports whether plain is the key behind hash.
func VerifyKey(plain, hash string) bool {
	if hash == "" || !strings.HasPrefix(plain, keyPrefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashKey(plain)), []byte(hash)) == 1
}
