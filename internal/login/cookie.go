package login

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"
)

type sessionPayload struct {
	UID  string   `json:"uid"`
	Tags []string `json:"tags"`
	Exp  int64    `json:"exp"`
}

const sessionTTL = 7 * 24 * time.Hour

func deriveKey(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// encryptSession returns a base64url AES-256-GCM ciphertext embedding
// the userID, filter tag IDs, and an expiry timestamp.
func encryptSession(secret, userID string, tagIDs []string, ttl time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("empty session secret")
	}
	if tagIDs == nil {
		tagIDs = []string{}
	}
	plaintext, err := json.Marshal(sessionPayload{
		UID:  userID,
		Tags: tagIDs,
		Exp:  time.Now().Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plaintext, nil)), nil
}

// decryptSession authenticates and decrypts an encrypted session cookie.
// Returns an error if the secret is wrong, the value was tampered with,
// or the embedded expiry has passed.
func decryptSession(secret, encoded string) (userID string, tagIDs []string, err error) {
	if secret == "" {
		return "", nil, errors.New("empty session secret")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, err
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return "", nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", nil, errors.New("invalid session")
	}
	plaintext, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", nil, errors.New("invalid session")
	}
	var p sessionPayload
	if err := json.Unmarshal(plaintext, &p); err != nil {
		return "", nil, err
	}
	if time.Now().Unix() > p.Exp {
		return "", nil, errors.New("session expired")
	}
	return p.UID, p.Tags, nil
}
