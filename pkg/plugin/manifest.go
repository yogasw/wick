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
	SchemaVersion int              `json:"schema_version"`
	Version       string           `json:"version"`
	ProtoVersion  int              `json:"proto_version"`
	Entry         string           `json:"entry"`
	OSArch        []string         `json:"os_arch"`
	SHA256        string           `json:"sha256"`
	Signature     string           `json:"signature"`
	Module        connector.Module `json:"module"`
}

// ManifestSchemaVersion is the current envelope format version.
const ManifestSchemaVersion = 1

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
	if m.ProtoVersion != ProtoVersion {
		return fmt.Errorf("proto v%d unsupported (host speaks v%d)", m.ProtoVersion, ProtoVersion)
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
