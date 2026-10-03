package plugin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Token prefixes make leaked tokens recognisable (and greppable in logs a
// plugin must not write them to).
const (
	accessPrefix   = "wick_svc_"
	callbackPrefix = "wick_plg_"
)

// AccessToken is one admin-generated bearer for a service's token routes.
// Only the hash is kept; the plaintext is shown once.
type AccessToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	Hint      string    `json:"hint"` // last 4 chars, for telling tokens apart
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

type tokenFile struct {
	Access map[string][]AccessToken `json:"access"`
	// CallbackRevoked lists services whose callback token was revoked; they
	// spawn without WICK_PLUGIN_TOKEN until an admin enables it again.
	CallbackRevoked map[string]bool `json:"callback_revoked"`
}

// Tokens holds the access tokens (persisted, hashed) and the live callback
// tokens (in memory, one per running process) of every service plugin.
type Tokens struct {
	path string

	mu       sync.Mutex
	f        tokenFile
	callback map[string]callbackEntry // sha256(token) → entry
}

type callbackEntry struct {
	key    string
	scopes []string
}

// NewTokens loads path ("" = memory only).
func NewTokens(path string) *Tokens {
	t := &Tokens{path: path, callback: map[string]callbackEntry{}}
	t.f = tokenFile{Access: map[string][]AccessToken{}, CallbackRevoked: map[string]bool{}}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &t.f)
		}
	}
	if t.f.Access == nil {
		t.f.Access = map[string][]AccessToken{}
	}
	if t.f.CallbackRevoked == nil {
		t.f.CallbackRevoked = map[string]bool{}
	}
	return t
}

func hashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func randToken(prefix string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (t *Tokens) save() error {
	if t.path == "" {
		return nil
	}
	b, _ := json.MarshalIndent(t.f, "", "  ")
	_ = os.MkdirAll(filepath.Dir(t.path), 0o700)
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.path)
}

// List returns key's access tokens (hashes included; callers strip them).
func (t *Tokens) List(key string) []AccessToken {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]AccessToken(nil), t.f.Access[key]...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Generate makes a new access token for key and returns its plaintext —
// the only time it is ever available.
func (t *Tokens) Generate(key, name string) (AccessToken, string, error) {
	plain := randToken(accessPrefix)
	at := AccessToken{ID: randToken("")[:12], Name: name, Hash: hashToken(plain), Hint: plain[len(plain)-4:], CreatedAt: time.Now().UTC()}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.f.Access[key] = append(t.f.Access[key], at)
	return at, plain, t.save()
}

// ErrTokenNotFound is returned for an unknown token id.
var ErrTokenNotFound = errors.New("token not found")

// Rotate replaces token id's secret, keeping its name; the old secret stops
// working at once.
func (t *Tokens) Rotate(key, id string) (AccessToken, string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, at := range t.f.Access[key] {
		if at.ID == id {
			plain := randToken(accessPrefix)
			at.Hash, at.Hint, at.CreatedAt, at.LastUsed = hashToken(plain), plain[len(plain)-4:], time.Now().UTC(), time.Time{}
			t.f.Access[key][i] = at
			return at, plain, t.save()
		}
	}
	return AccessToken{}, "", ErrTokenNotFound
}

// Revoke deletes token id.
func (t *Tokens) Revoke(key, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	list := t.f.Access[key]
	for i, at := range list {
		if at.ID == id {
			t.f.Access[key] = append(list[:i:i], list[i+1:]...)
			return t.save()
		}
	}
	return ErrTokenNotFound
}

// Verify reports whether bearer is a live access token of key.
func (t *Tokens) Verify(key, bearer string) bool {
	if bearer == "" {
		return false
	}
	h := hashToken(bearer)
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, at := range t.f.Access[key] {
		if subtle.ConstantTimeCompare([]byte(at.Hash), []byte(h)) == 1 {
			t.f.Access[key][i].LastUsed = time.Now().UTC()
			return true
		}
	}
	return false
}

// IssueCallback makes the callback token for a new process of key (scoped
// to scopes), dropping the previous one. ok is false when the admin revoked
// key's callback access.
func (t *Tokens) IssueCallback(key string, scopes []string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropCallback(key)
	if t.f.CallbackRevoked[key] {
		return "", false
	}
	plain := randToken(callbackPrefix)
	t.callback[hashToken(plain)] = callbackEntry{key: key, scopes: append([]string(nil), scopes...)}
	return plain, true
}

func (t *Tokens) dropCallback(key string) {
	for h, e := range t.callback {
		if e.key == key {
			delete(t.callback, h)
		}
	}
}

// SetCallbackRevoked revokes (true) or re-allows key's callback token. A
// revoke takes effect immediately; re-allowing issues a token on the next
// (re)start.
func (t *Tokens) SetCallbackRevoked(key string, revoked bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if revoked {
		t.f.CallbackRevoked[key] = true
		t.dropCallback(key)
	} else {
		delete(t.f.CallbackRevoked, key)
	}
	return t.save()
}

// CallbackRevoked reports whether key's callback access is revoked.
func (t *Tokens) CallbackRevoked(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.f.CallbackRevoked[key]
}

// LookupCallback resolves a callback bearer to its plugin and scopes.
func (t *Tokens) LookupCallback(bearer string) (key string, scopes []string, ok bool) {
	if bearer == "" {
		return "", nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.callback[hashToken(bearer)]
	return e.key, e.scopes, ok
}
