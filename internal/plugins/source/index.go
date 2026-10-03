// Package source installs and updates plugins from remote sources: a
// plugins.json URL, a direct .zip link, or GitHub releases (public or private
// with a PAT). Every zip goes through the same verification as a local
// install — zip_sha256 from the index, then the binary sha256 + signature from
// the manifest inside the zip — before it lands in its kind folder.
package source

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// Asset is one os/arch build of a plugin in plugins.json v2. The v1 index
// carried a bare download URL string; UnmarshalJSON accepts both.
type Asset struct {
	URL       string `json:"url"`
	ZipSHA256 string `json:"zip_sha256,omitempty"`
	Signature string `json:"signature,omitempty"`
	// apiURL is the GitHub asset API endpoint, set for private repos where
	// browser_download_url does not work.
	apiURL string
}

func (a *Asset) UnmarshalJSON(b []byte) error {
	if b = bytes.TrimSpace(b); len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &a.URL)
	}
	type plain Asset
	return json.Unmarshal(b, (*plain)(a))
}

// Entry is one plugin in plugins.json (always an array, even for one plugin).
type Entry struct {
	Key          string              `json:"key"`
	Kind         string              `json:"kind,omitempty"`
	Name         string              `json:"name,omitempty"`
	Description  string              `json:"description,omitempty"`
	Version      string              `json:"version"`
	ProtoVersion int                 `json:"proto_version,omitempty"`
	Assets       map[string]Asset    `json:"assets"`
	DefaultTags  []entity.DefaultTag `json:"default_tags,omitempty"`
}

// ParseIndex parses plugins.json and resolves relative asset URLs against
// base (the URL plugins.json was fetched from). Entries without a key are
// dropped; v1 entries that only carry "name" get it as their key.
func ParseIndex(raw []byte, base string) ([]Entry, error) {
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse plugins.json: %w", err)
	}
	baseURL, _ := url.Parse(base)
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Key == "" {
			e.Key = e.Name
		}
		if e.Key == "" {
			continue
		}
		if e.Name == "" {
			e.Name = e.Key
		}
		for oa, a := range e.Assets {
			a.URL = resolveURL(baseURL, a.URL)
			e.Assets[oa] = a
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func resolveURL(base *url.URL, ref string) string {
	if base == nil || ref == "" {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil || u.IsAbs() {
		return ref
	}
	return base.ResolveReference(u).String()
}

// ValidateIndex reports the first schema problem: invalid or duplicate key,
// non-semver version, unknown kind.
func ValidateIndex(entries []Entry) error {
	if len(entries) == 0 {
		return fmt.Errorf("index has no plugins")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if err := wickplugin.ValidateKey(e.Key); err != nil {
			return fmt.Errorf("%q: %w", e.Key, err)
		}
		if seen[e.Key] {
			return fmt.Errorf("duplicate key %q", e.Key)
		}
		seen[e.Key] = true
		if !semver.IsValid("v" + strings.TrimPrefix(e.Version, "v")) {
			return fmt.Errorf("%q: version %q is not semver", e.Key, e.Version)
		}
		if e.Kind != "" && wickplugin.NormalizeKind(e.Kind) != e.Kind {
			return fmt.Errorf("%q: unknown kind %q", e.Key, e.Kind)
		}
	}
	return nil
}

// zipNameOK reports whether an asset file name follows
// <key>-<version>-<os>-<arch>.zip for the given entry and os/arch.
func zipNameOK(e Entry, osArch, assetURL string) bool {
	want := fmt.Sprintf("%s-%s-%s.zip", e.Key, strings.TrimPrefix(e.Version, "v"), strings.ReplaceAll(osArch, "/", "-"))
	name := path.Base(assetURL)
	if u, err := url.Parse(assetURL); err == nil {
		name = path.Base(u.Path)
	}
	return name == want
}

// parseZipName splits "<key>-<version>-<os>-<arch>.zip" (keys never contain
// "-"), used when a source is a bare .zip link or a release has no index.
func parseZipName(name string) (key, version, osArch string, ok bool) {
	if !strings.HasSuffix(name, ".zip") {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimSuffix(name, ".zip"), "-")
	if len(parts) < 4 {
		return "", "", "", false
	}
	n := len(parts)
	return parts[0], strings.Join(parts[1:n-2], "-"), parts[n-2] + "/" + parts[n-1], true
}

// newer reports whether version a is strictly newer than b (semver, "v"
// optional). Unparseable versions never count as newer.
func newer(a, b string) bool {
	na, nb := "v"+strings.TrimPrefix(a, "v"), "v"+strings.TrimPrefix(b, "v")
	if !semver.IsValid(na) {
		return false
	}
	if !semver.IsValid(nb) {
		return true
	}
	return semver.Compare(na, nb) > 0
}

// keyAllowed applies a source's comma-separated key filter.
func keyAllowed(filter, key string) bool {
	if strings.TrimSpace(filter) == "" {
		return true
	}
	for _, k := range strings.Split(filter, ",") {
		if strings.TrimSpace(k) == key {
			return true
		}
	}
	return false
}
