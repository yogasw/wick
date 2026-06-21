package plugin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// Version is the connector release version stamped into the manifest. It is
// overridden at build time via -ldflags "-X .../pkg/plugin.Version=1.2.3".
var Version = "0.0.0-dev"

// TrustedPubKey is the publisher's base64 ed25519 public key baked into a
// released wick binary via -ldflags. Empty in dev builds.
var TrustedPubKey string

// RequireSignature, when true (set via ldflags in release builds), makes the
// loader reject any plugin without a valid signature from a trusted key. The
// env var WICK_PLUGIN_REQUIRE_SIGNATURE ("1"/"0") overrides it at runtime.
var RequireSignature bool

// RequireSig reports the effective signature-required policy (env overrides
// the baked default).
func RequireSig() bool {
	switch os.Getenv("WICK_PLUGIN_REQUIRE_SIGNATURE") {
	case "1", "true":
		return true
	case "0", "false":
		return false
	}
	return RequireSignature
}

// TrustedKeys returns every trusted base64 public key: the baked
// TrustedPubKey plus any in WICK_PLUGIN_PUBKEY (comma-separated).
func TrustedKeys() []string {
	var out []string
	if TrustedPubKey != "" {
		out = append(out, TrustedPubKey)
	}
	for _, k := range strings.Split(os.Getenv("WICK_PLUGIN_PUBKEY"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// GenerateKeypair returns a fresh ed25519 keypair as base64 strings
// (private = 64-byte seed||pub, public = 32-byte).
func GenerateKeypair() (privB64, pubB64 string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", ""
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub)
}

// SignSHA256 reads a base64 ed25519 private key from privKeyPath and signs
// the sha256 hex string, returning the base64 signature.
func SignSHA256(privKeyPath, sha256hex string) (string, error) {
	raw, err := os.ReadFile(privKeyPath)
	if err != nil {
		return "", fmt.Errorf("read signing key: %w", err)
	}
	return signSHA256WithKey(strings.TrimSpace(string(raw)), sha256hex)
}

func signSHA256WithKey(privB64, sha256hex string) (string, error) {
	keyBytes, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return "", fmt.Errorf("decode signing key: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("signing key must be %d bytes, got %d", ed25519.PrivateKeySize, len(keyBytes))
	}
	sig := ed25519.Sign(ed25519.PrivateKey(keyBytes), []byte(sha256hex))
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifySHA256 reports whether sigB64 is a valid signature of sha256hex by
// ANY of the trusted base64 public keys. Bad inputs return false (no panic).
func VerifySHA256(trustedPubsB64 []string, sha256hex, sigB64 string) bool {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) == 0 {
		return false
	}
	for _, pb := range trustedPubsB64 {
		pub, err := base64.StdEncoding.DecodeString(pb)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			continue
		}
		if ed25519.Verify(ed25519.PublicKey(pub), []byte(sha256hex), sig) {
			return true
		}
	}
	return false
}
