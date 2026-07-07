package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yogasw/wick/pkg/safeexec"
)

// errCosignNotFound signals the cosign CLI is absent so the caller can soft-skip
// with a warning instead of failing the build.
var errCosignNotFound = errors.New("cosign not found on PATH")

// cosignSignBinary signs binPath with the cosign CLI using the key at keyPath,
// writing <binName>.sig and <binName>.pem into outDir. cosign is intentionally
// invoked as an EXTERNAL tool (not the sigstore Go libraries) so plugin binaries
// stay lean — pulling sigstore-go into pkg/plugin would bloat every connector,
// the opposite of the size work. Returns the produced sidecar paths.
//
// Soft-skip: when cosign is not installed, returns errCosignNotFound so the
// build can continue (cosign signing is opt-in, like the ed25519 --sign-key).
func cosignSignBinary(binPath, keyPath, outDir, binName string) (sig, cert string, err error) {
	if _, lookErr := safeexec.LookPath("cosign"); lookErr != nil {
		return "", "", errCosignNotFound
	}
	if _, statErr := os.Stat(keyPath); statErr != nil {
		return "", "", fmt.Errorf("cosign key %q: %w", keyPath, statErr)
	}
	sigPath := filepath.Join(outDir, binName+".sig")
	certPath := filepath.Join(outDir, binName+".pem")

	// cosign sign-blob signs an arbitrary file (our binary). COSIGN_PASSWORD is
	// honored from the environment; --yes skips the confirmation prompt so this
	// runs unattended in CI.
	cmd := safeexec.Command("cosign", "sign-blob",
		"--key", keyPath,
		"--output-signature", sigPath,
		"--output-certificate", certPath,
		"--yes",
		binPath,
	)
	cmd.Stdout = os.Stderr // cosign prints the sig to stdout too; keep our stdout clean
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if runErr := cmd.Run(); runErr != nil {
		return "", "", fmt.Errorf("cosign sign-blob: %w", runErr)
	}
	return sigPath, certPath, nil
}
