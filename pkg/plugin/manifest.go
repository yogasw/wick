package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/yogasw/wick/pkg/connector"
)

// Manifest is the on-disk plugin.json envelope: the connector module plus the
// distribution metadata the host needs to verify and load it. The connector
// module marshals with its func fields excluded (json:"-"), so the envelope
// is fully round-trippable.
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	// Kind is the plugin kind: "connector" (default), "tool", or "job". The
	// platform routes installed plugins by kind into the matching registry;
	// all kinds share the same gRPC service (Execute(op,args)→result is generic
	// enough for a tool's Run(input) and a job's Run(trigger)). Empty = the
	// pre-kind default "connector" for backward compatibility.
	Kind         string           `json:"kind,omitempty"`
	Version      string           `json:"version"`
	ProtoVersion int              `json:"proto_version"`
	Entry        string           `json:"entry"`
	OSArch       []string         `json:"os_arch"`
	SHA256       string           `json:"sha256"`
	Signature    string           `json:"signature"`
	Module       connector.Module `json:"module"`
}

// ManifestSchemaVersion is the current envelope format version.
const ManifestSchemaVersion = 1

// Plugin kinds. connector is the default and the only kind with a host-side
// execution adapter today; tool/job are accepted by the manifest + build
// tooling so the layout and CLI are forward-compatible (§18).
const (
	KindConnector = "connector"
	KindTool      = "tool"
	KindJob       = "job"
)

// NormalizeKind returns a valid kind, defaulting empty/unknown to connector.
func NormalizeKind(k string) string {
	switch k {
	case KindTool, KindJob:
		return k
	default:
		return KindConnector
	}
}

// ValidateKey enforces that a plugin's Meta.Key is a safe slug. Key is the one
// identity used everywhere — the source folder, the zip name, the on-disk
// install dir (DefaultDir/<key>), and the runtime registry key — so it must be a
// plain lowercase slug with no path separators or traversal (it becomes a
// directory name; a key like "../x" or "a/b" would escape the plugins dir).
// Enforced at BOTH build time and install time so a hand-written manifest can't
// sneak a bad key past the build.
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("plugin key is empty")
	}
	if len(key) > 64 {
		return fmt.Errorf("plugin key %q too long (max 64)", key)
	}
	// No '-': the release asset name is "<key>-<version>-<goos>-<goarch>.zip" and
	// the catalog parses it by splitting on '-', so a '-' in the key would make
	// the os/arch split ambiguous. Use '_' for multi-word keys (google_workspace).
	for _, r := range key {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if !ok {
			return fmt.Errorf("plugin key %q invalid: use lowercase letters, digits, or '_' only (no '-', spaces, slashes, or dots — '-' would break the zip-name split)", key)
		}
	}
	return nil
}

// sha256File returns the hex sha256 of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open binary: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash binary: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyManifest checks a plugin envelope against the binary at binaryPath
// and the host's trust policy. Returns nil when the plugin is safe to load.
// Order: os_arch -> proto_version -> sha256 integrity -> signature.
func VerifyManifest(m Manifest, binaryPath string) error {
	host := runtime.GOOS + "/" + runtime.GOARCH
	okArch := false
	for _, oa := range m.OSArch {
		if oa == host {
			okArch = true
			break
		}
	}
	if !okArch {
		return fmt.Errorf("incompatible arch: need %v, have %s", m.OSArch, host)
	}
	if !ProtoVersionSupported(m.ProtoVersion) {
		return fmt.Errorf("proto v%d unsupported (host speaks v%d–v%d)", m.ProtoVersion, MinProtoVersion, ProtoVersion)
	}
	sum, err := sha256File(binaryPath)
	if err != nil {
		return err
	}
	if sum != m.SHA256 {
		return fmt.Errorf("integrity check failed: binary sha256 %s != manifest %s", sum, m.SHA256)
	}
	if RequireSig() {
		if m.Signature == "" {
			return fmt.Errorf("signature required but plugin is unsigned")
		}
		if !VerifySHA256(TrustedKeys(), m.SHA256, m.Signature) {
			return fmt.Errorf("signature verification failed (no trusted key matched)")
		}
		return nil
	}
	if m.Signature != "" {
		if keys := TrustedKeys(); len(keys) > 0 && !VerifySHA256(keys, m.SHA256, m.Signature) {
			return fmt.Errorf("signature present but does not match any trusted key")
		}
	}
	return nil
}

// BuildSelfManifest builds the envelope for a connector plugin binary from
// the binary itself (self-pack): it hashes os.Executable() and, when
// signKeyPath is non-empty, signs that hash. mod is the connector's module.
func BuildSelfManifest(mod connector.Module, signKeyPath string) (Manifest, error) {
	exe, err := os.Executable()
	if err != nil {
		return Manifest{}, fmt.Errorf("locate self: %w", err)
	}
	sum, err := sha256File(exe)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Kind:          KindConnector, // build tooling overrides this from --kind
		Version:       Version,
		ProtoVersion:  ProtoVersion,
		Entry:         filepath.Base(exe),
		OSArch:        []string{runtime.GOOS + "/" + runtime.GOARCH},
		SHA256:        sum,
		Module:        mod,
	}
	if signKeyPath != "" {
		sig, err := SignSHA256(signKeyPath, sum)
		if err != nil {
			return Manifest{}, err
		}
		m.Signature = sig
	}
	return m, nil
}
