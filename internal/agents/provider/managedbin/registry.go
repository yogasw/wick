// Package managedbin installs, updates and rolls back provider CLI
// binaries that wick manages itself, under
//
//	<wick data dir>/providers/bin/<type>/
//	  versions/<ver>/<binary>   one folder per installed version
//	  current                   name of the active version (atomic text file)
//	  state.json                versions, sha256, host, install time, last job
//
// Nothing here ever writes to PATH or touches a binary installed by hand;
// an instance with a Binary override keeps using that. Nothing runs by
// itself either: a download only happens when an admin asks for one
// (Install / Update / a specific version). Checking for a newer release
// only raises a flag.
//
// The install order is fixed and must not be reordered (see install.go):
// download to .partial → sha256 against the GitHub digest (+ any extra
// cross-check the source adds) → only then `--version` in a sandbox →
// version must equal the requested tag → rename into versions/<ver> →
// move `current`. A file that failed verification is deleted and never
// executed.
//
// Adding a provider = one ReleaseSource implementation in that provider's
// package + Register(type, source) in its init(). The install/update/
// rollback flow and the UI are type-agnostic.
package managedbin

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// VersionContract is how wick learns a CLI's version: the argv to run and
// how to read the output. It lives in the provider's own package; both
// the managed installer and provider.Probe use it, so there is one source.
type VersionContract struct {
	// Args is the argv after the binary. Usually {"--version"}.
	Args []string
	// Parse extracts the bare semver ("18.4.3") from the command output.
	Parse func(out string) (string, bool)
}

// MatchesTag reports whether a parsed version is the release tag
// ("v18.4.3" ↔ "18.4.3").
func MatchesTag(tag, version string) bool {
	t := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	return t != "" && t == strings.TrimSpace(version)
}

// TagVersion is the bare version of a release tag.
func TagVersion(tag string) string { return strings.TrimPrefix(strings.TrimSpace(tag), "v") }

var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?`)

// FirstSemver returns the first semver-looking token in the first
// non-empty line of out. The default parser for CLIs that print
// "<ver>" or "<ver> (Something)".
func FirstSemver(out string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := semverRe.FindString(line); m != "" {
			return m, true
		}
		return "", false
	}
	return "", false
}

// Asset is one downloadable file of a release.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"` // hex, from the API digest; "" when GitHub gave none
}

// Release is one published release.
type Release struct {
	Tag        string  `json:"tag"`
	Version    string  `json:"version"`
	Published  string  `json:"published_at,omitempty"`
	Prerelease bool    `json:"prerelease,omitempty"`
	Assets     []Asset `json:"-"`
}

// Fetcher is the narrow HTTP surface a source may use for extra checks
// (omp's SHA256SUMS.txt). It enforces the same host allowlist and size
// limit as the main download.
type Fetcher interface {
	FetchSmall(ctx context.Context, url string, max int64) ([]byte, error)
}

// ReleaseSource is everything type-specific about a managed binary.
type ReleaseSource interface {
	// Binary is the executable's file name inside versions/<ver>/.
	Binary() string
	// Repo is "owner/name" on GitHub. URLs are built from it, never from
	// user input.
	Repo() string
	// PickAsset chooses the asset for host, or explains why none fits.
	PickAsset(h Host, assets []Asset) (Asset, error)
	// Unpack turns the downloaded (already verified) file into the binary
	// at dest: a raw binary is renamed, an archive has its member extracted.
	Unpack(downloaded, dest string) error
	// CrossCheck runs any extra integrity check on top of the API digest
	// (e.g. a SHA256SUMS file). nil = none.
	CrossCheck(ctx context.Context, f Fetcher, rel Release, asset Asset, sha256hex string) error
	// Contract is how the installed binary reports its version.
	Contract() VersionContract
}

var (
	regMu     sync.RWMutex
	sources   = map[string]ReleaseSource{}
	contracts = map[string]VersionContract{}
)

// Register makes typ a wick-managed binary type. Also registers the
// source's version contract.
func Register(typ string, src ReleaseSource) {
	regMu.Lock()
	defer regMu.Unlock()
	sources[typ] = src
	contracts[typ] = src.Contract()
}

// RegisterContract registers only a version contract, for types wick
// probes but does not manage (claude/codex/gemini).
func RegisterContract(typ string, c VersionContract) {
	regMu.Lock()
	defer regMu.Unlock()
	contracts[typ] = c
}

// Lookup returns the managed source for typ.
func Lookup(typ string) (ReleaseSource, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	s, ok := sources[typ]
	return s, ok
}

// ContractFor returns the version contract for typ.
func ContractFor(typ string) (VersionContract, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	c, ok := contracts[typ]
	return c, ok
}

// Types lists the registered managed types, sorted.
func Types() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(sources))
	for t := range sources {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
