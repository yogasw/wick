// Package enc implements wick's encrypted-fields layer: a per-user
// AES-256-GCM cipher that lets credentials and other sensitive values
// flow between an LLM and a connector without ever appearing as
// plaintext in the LLM's context window or in audit logs.
//
// The wire format is a single token with a fixed prefix:
//
//	wick_enc_<base64url(nonce ‖ AES-256-GCM(plaintext, nonce, derived_key))>
//
// The 32-byte derived key is per-user — HKDF(master_key, salt=user_uuid,
// info="wick-enc") — so a token issued for user A cannot be decrypted
// when user B replays it.
//
// Two operation modes:
//
//   - Mask(data, values, user) — replaces every occurrence of each
//     plaintext value in data with its wick_enc_ token, sharing one
//     token per identical plaintext within the same call so the LLM
//     does not mistake duplicates for distinct credentials.
//   - Unmask(data, user) — inverse: scans data for wick_enc_ tokens
//     and replaces each with its plaintext.
//
// Disabled() reports whether WICK_ENC_DISABLE is set — when true,
// callers should skip mask/unmask and pass values through unchanged.
package enc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/hkdf"
)

// Prefix marks an encrypted token in user-visible payloads. The string
// is intentionally distinct from any other format wick or common
// upstream APIs emit, so a substring scan is unambiguous.
const Prefix = "wick_enc_"

// MasterPrefix marks a config-encrypted token (at-rest, master-key
// keyed, no per-user salt). Distinct from Prefix so the wrong decrypt
// path can't pick it up — Mask/Unmask scan only for Prefix.
const MasterPrefix = "wick_cenc_"

// Service wraps the master key + the per-user key derivation. One
// instance is shared across the process; methods are safe to call
// concurrently.
type Service struct {
	masterKey []byte
	disabled  bool

	keyMu sync.Mutex
	// keyCache memoises HKDF(masterKey, salt=userUUID, info="wick-enc")
	// per user_uuid. The derived key is 32 bytes and HKDF is cheap, but
	// caching keeps Execute fast on hot paths (encrypt cache + per-user
	// derive on every request would still beat key-per-encrypt).
	keyCache map[string][]byte
}

// New constructs a Service from a hex-encoded 32-byte master key. An
// empty key (or one of the wrong length) returns an error so the boot
// path can fail loudly rather than silently disabling encryption.
//
// When WICK_ENC_DISABLE=true is set in the environment, the returned
// Service is valid but Disabled() returns true; callers MUST short-
// circuit on that flag rather than letting empty/zero crypto run.
func New(masterKeyHex string) (*Service, error) {
	if disabledFromEnv() {
		return &Service{disabled: true, keyCache: map[string][]byte{}}, nil
	}
	if masterKeyHex == "" {
		return nil, errors.New("enc: master key is empty")
	}
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("enc: master key not hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("enc: master key must be 32 bytes, got %d", len(key))
	}
	return &Service{
		masterKey: key,
		keyCache:  map[string][]byte{},
	}, nil
}

// Disabled reports whether encryption is opt-out via the env var. When
// true, Mask/Unmask return data unchanged and EncryptValue/DecryptValue
// refuse to operate.
func (s *Service) Disabled() bool { return s.disabled }

// disabledFromEnv reads WICK_ENC_DISABLE — anything truthy disables.
func disabledFromEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("WICK_ENC_DISABLE")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// derivedKey returns the per-user 32-byte AES key, computing and
// caching it on first request. Empty userUUID is allowed and produces
// a stable "no-user" key — useful for system-side encrypt calls but
// callers should prefer a real user identity wherever possible.
func (s *Service) derivedKey(userUUID string) ([]byte, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if k, ok := s.keyCache[userUUID]; ok {
		return k, nil
	}
	r := hkdf.New(newSHA256, s.masterKey, []byte(userUUID), []byte("wick-enc"))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("hkdf derive: %w", err)
	}
	s.keyCache[userUUID] = out
	return out, nil
}

// EncryptValue produces one wick_enc_ token for the given plaintext.
// Returns the original string when the service is disabled. No length
// floor — admin opted in (manual UI) or tagged the field as
// secret/encrypt (auto-mask), so short values are honored either way.
// Pick distinct, non-generic values for sensitive fields to avoid
// false-positive substring hits during the auto-mask scan.
func (s *Service) EncryptValue(plain, userUUID string) (string, error) {
	if s.disabled {
		return plain, nil
	}
	key, err := s.derivedKey(userUUID)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm new: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	buf := make([]byte, 0, len(nonce)+len(ct))
	buf = append(buf, nonce...)
	buf = append(buf, ct...)
	return Prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// DecryptValue reverses EncryptValue. Returns ErrNotToken when the
// input lacks the wick_enc_ prefix and an opaque error when the token
// is malformed or the key is wrong (e.g. cross-user replay).
func (s *Service) DecryptValue(token, userUUID string) (string, error) {
	if !IsToken(token) {
		return "", ErrNotToken
	}
	if s.disabled {
		return "", errors.New("enc: service disabled")
	}
	key, err := s.derivedKey(userUUID)
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(Prefix):])
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm new: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("enc: ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("gcm open: %w", err)
	}
	return string(plain), nil
}

// ErrNotToken signals that DecryptValue saw something that does not
// carry the wick_enc_ prefix. Callers that scan free-form text for
// embedded tokens use IsToken first; this error is for direct callers
// that expected a token.
var ErrNotToken = errors.New("enc: not a wick_enc_ token")

// IsToken reports whether s begins with the wick_enc_ prefix. Cheap —
// callers can use it to skip the work of decoding when they're not
// sure what they're holding.
func IsToken(s string) bool {
	return strings.HasPrefix(s, Prefix)
}

// IsMasterToken reports whether s carries the wick_cenc_ prefix used
// for at-rest config encryption. Distinct from IsToken so callers
// can pick the right decrypt path (master vs per-user).
func IsMasterToken(s string) bool {
	return strings.HasPrefix(s, MasterPrefix)
}

// masterKeyFor returns the AES key used by EncryptMaster/DecryptMaster.
// Domain-separated from per-user keys via HKDF info="wick-cenc-master"
// so the same masterKey can never accidentally encrypt-or-decrypt
// across the two layers.
func (s *Service) masterKeyFor() ([]byte, error) {
	r := hkdf.New(newSHA256, s.masterKey, nil, []byte("wick-cenc-master"))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("hkdf master derive: %w", err)
	}
	return out, nil
}

// EncryptMaster produces a wick_cenc_ token bound to the master key
// (no per-user salt). Used for at-rest config encryption: admin pastes
// plaintext into a `secret`-tagged config row, the configs service
// stores the token, and DecryptMaster restores the plaintext when the
// row is read back into the cache.
//
// Disabled service is a programmer error — the configs layer must not
// call this when Disabled() is true. Returns an error rather than
// silently passing through.
func (s *Service) EncryptMaster(plain string) (string, error) {
	if s.disabled {
		return "", errors.New("enc: service disabled")
	}
	key, err := s.masterKeyFor()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm new: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	buf := make([]byte, 0, len(nonce)+len(ct))
	buf = append(buf, nonce...)
	buf = append(buf, ct...)
	return MasterPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// DecryptMaster reverses EncryptMaster. Returns ErrNotToken when the
// input lacks the wick_cenc_ prefix. The error is opaque on
// auth/decrypt failure (typically: master key was rotated since the
// token was written).
func (s *Service) DecryptMaster(token string) (string, error) {
	if !IsMasterToken(token) {
		return "", ErrNotToken
	}
	if s.disabled {
		return "", errors.New("enc: service disabled")
	}
	key, err := s.masterKeyFor()
	if err != nil {
		return "", err
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(MasterPrefix):])
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm new: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("enc: ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("gcm open: %w", err)
	}
	return string(plain), nil
}

// Mask replaces every occurrence of every value in `values` inside
// `data` with its wick_enc_ token. Identical values receive identical
// tokens within one call — the per-call cache guarantees the LLM does
// not see two distinct ciphertexts for the same secret and conclude
// they are different secrets.
//
// When the service is disabled, returns data unchanged.
func (s *Service) Mask(data string, values []string, userUUID string) string {
	if s.disabled || data == "" || len(values) == 0 {
		return data
	}
	cache := make(map[string]string, len(values))
	out := data
	for _, v := range values {
		if v == "" || !strings.Contains(out, v) {
			continue
		}
		token, ok := cache[v]
		if !ok {
			t, err := s.EncryptValue(v, userUUID)
			if err != nil || t == v {
				continue
			}
			cache[v] = t
			token = t
		}
		out = strings.ReplaceAll(out, v, token)
	}
	return out
}

// MaskIgnoreCase is the case-insensitive variant of Mask. Every case
// variant of a keyword in `data` is replaced with a single token derived
// from the keyword's configured form (so the LLM, when it passes the
// token back, gets the configured spelling on decrypt). Keywords that
// differ only in case share one token via a lowercased cache key.
//
// Implementation uses regexp with the (?i) flag and quotes the keyword
// so regex metacharacters in user-supplied values are matched literally.
func (s *Service) MaskIgnoreCase(data string, values []string, userUUID string) string {
	if s.disabled || data == "" || len(values) == 0 {
		return data
	}
	cache := make(map[string]string, len(values))
	out := data
	for _, v := range values {
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		token, ok := cache[key]
		if !ok {
			t, err := s.EncryptValue(v, userUUID)
			if err != nil || t == v {
				continue
			}
			cache[key] = t
			token = t
		}
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(v))
		if err != nil {
			continue
		}
		out = re.ReplaceAllString(out, token)
	}
	return out
}

// tokenRegex matches wick_enc_<base64url> within free-form text. The
// base64url alphabet excludes "+/", uses "-_" instead, and has no
// padding (RawURLEncoding) — so a greedy [A-Za-z0-9_-]+ run captures
// the token exactly without colliding with surrounding JSON/quote
// characters.
var tokenRegex = regexp.MustCompile(`wick_enc_[A-Za-z0-9_\-]+`)

// Unmask scans data for wick_enc_ tokens and replaces each with its
// plaintext. Returns an error when ANY token fails to decrypt
// (typically: the key was rotated since the token was issued, or the
// token was issued for a different user). Tokens are de-duplicated so
// repeated occurrences only pay decryption cost once.
//
// When the service is disabled, returns data unchanged with no error.
func (s *Service) Unmask(data, userUUID string) (string, error) {
	if s.disabled || data == "" {
		return data, nil
	}
	tokens := tokenRegex.FindAllString(data, -1)
	if len(tokens) == 0 {
		return data, nil
	}
	seen := make(map[string]string, len(tokens))
	for _, tok := range tokens {
		if _, ok := seen[tok]; ok {
			continue
		}
		plain, err := s.DecryptValue(tok, userUUID)
		if err != nil {
			return data, fmt.Errorf("decrypt token: %w", err)
		}
		seen[tok] = plain
	}
	out := data
	for tok, plain := range seen {
		out = strings.ReplaceAll(out, tok, plain)
	}
	return out, nil
}
