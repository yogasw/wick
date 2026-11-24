// Package buildsource reports provenance for the binary actually running.
package buildsource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// Encoded is stamped by wick build for local source replacements. It contains
// only public repository coordinates and immutable revision metadata, no paths or credentials.
var Encoded string

const OfficialRepo = "yogasw/wick"

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Metadata struct {
	Kind string `json:"kind"`
	Repository string `json:"repository,omitempty"`
	Branch string `json:"branch,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Revision string `json:"revision,omitempty"`
	CommitDate string `json:"commit_date,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	Dirty bool `json:"dirty"`
	CompareRef string `json:"compare_ref,omitempty"`
	Tag string `json:"tag,omitempty"`
}

func Encode(m Metadata) string {
	b, _ := json.Marshal(m)
	return base64.RawURLEncoding.EncodeToString(b)
}

// GitHubRepository accepts GitHub remotes only; never return userinfo, tokens,
// arbitrary URLs or local checkout paths to the browser.
func GitHubRepository(remote string) string {
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "git@github.com:") {
		remote = strings.TrimPrefix(remote, "git@github.com:")
	} else {
		u, err := url.Parse(remote)
		if err != nil || u.Hostname() != "github.com" || (u.Scheme != "https" && u.Scheme != "ssh") {
			return ""
		}
		remote = strings.TrimPrefix(u.Path, "/")
	}
	remote = strings.TrimSuffix(remote, ".git")
	if !repoPattern.MatchString(remote) { return "" }
	return remote
}

// StableVersion is a published core version, not the synthetic local build
// counter or a prerelease. It deliberately does not guess from app versions.
func StableVersion(v string) string {
	v = "v" + strings.TrimPrefix(strings.TrimSpace(v), "v")
	if !semver.IsValid(v) || semver.Prerelease(v) != "" || semver.Build(v) != "" || semver.Canonical(v) != v { return "" }
	return v
}

func Current(wickVersion, appVersion, releaseRepo string) Metadata {
	if b, err := base64.RawURLEncoding.DecodeString(Encoded); err == nil && len(b) > 0 {
		var m Metadata
		if json.Unmarshal(b, &m) == nil && m.Kind == "source" && shaPattern.MatchString(m.Revision) {
			if !repoPattern.MatchString(m.Repository) { m.Repository = ""; m.CompareRef = ""; m.Upstream = "" }
			return m
		}
	}
	// For packaged releases the embedded Wick module version identifies its
	// tag even if the application was built in a git-less scaffold.
	if strings.Contains(appVersion, "-source-build") {
		// An old source binary without a stamp is not a packaged release.
		return Metadata{Kind: "source"}
	}
	version := StableVersion(wickVersion)
	if releaseRepo == OfficialRepo {
		if official := StableVersion(appVersion); official != "" { version = official }
	}
	if version != "" {
		return Metadata{Kind: "release", Repository: OfficialRepo, Tag: version, SourceVersion: version, CompareRef: "master", Upstream: "origin/master"}
	}
	return Metadata{Kind: "unknown"}
}

type Status struct {
	Metadata
	BuildTime string `json:"build_time,omitempty"`
	CheckedAt string `json:"checked_at"`
	Behind *int `json:"behind,omitempty"`
	Ahead *int `json:"ahead,omitempty"`
	LatestRelease string `json:"latest_release,omitempty"`
	NewerReleases *int `json:"newer_releases,omitempty"`
	CompareError string `json:"compare_error,omitempty"`
	ReleaseError string `json:"release_error,omitempty"`
}

type Checker struct {
	client *http.Client
	api string
	meta Metadata
	builtAt string
	mu sync.Mutex
	cached *Status
	expires time.Time
}

func NewChecker(meta Metadata, builtAt string) *Checker {
	return &Checker{client: &http.Client{Timeout: 8*time.Second}, api: "https://api.github.com", meta: meta, builtAt: builtAt}
}

func (c *Checker) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api+path, nil)
	if err != nil { return err }
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return fmt.Errorf("GitHub HTTP %d", resp.StatusCode) }
	return json.NewDecoder(resp.Body).Decode(dst)
}

// Snapshot performs at most one check per five minutes per process, only on
// explicit detail requests. Failures remain unknown, never zero.
func (c *Checker) Snapshot(ctx context.Context) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil && time.Now().Before(c.expires) { return *c.cached }
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s := Status{Metadata: c.meta, BuildTime: c.builtAt}
	c.check(ctx, &s)
	s.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	c.cached = &s
	c.expires = time.Now().Add(5*time.Minute)
	return s
}

func (c *Checker) check(ctx context.Context, s *Status) {
	if s.Kind == "release" && s.Tag != "" {
		var commit struct {
			SHA string `json:"sha"`
			Commit struct { Committer struct { Date string `json:"date"` } `json:"committer"` } `json:"commit"`
		}
		if c.get(ctx, "/repos/"+OfficialRepo+"/commits/"+url.PathEscape(s.Tag), &commit) == nil && shaPattern.MatchString(commit.SHA) {
			s.Revision = commit.SHA
			s.CommitDate = commit.Commit.Committer.Date
		}
	}
	if s.Repository != "" && repoPattern.MatchString(s.Repository) && s.CompareRef != "" && shaPattern.MatchString(s.Revision) {
		var cmp struct {
			Ahead *int `json:"ahead_by"`
			Behind *int `json:"behind_by"`
		}
		path := "/repos/"+s.Repository+"/compare/"+s.Revision+"..."+url.PathEscape(s.CompareRef)
		if err := c.get(ctx, path, &cmp); err != nil || cmp.Ahead == nil || cmp.Behind == nil {
			s.CompareError = "Origin comparison unavailable (unpublished commit, deleted ref, or network error)"
		} else {
			// Base is installed SHA, head is origin. GitHub ahead_by counts
			// commits on origin that are missing from the installed build.
			s.Behind, s.Ahead = cmp.Ahead, cmp.Behind
		}
	} else {
		s.CompareError = "No comparable published revision and upstream"
	}

	base := StableVersion(s.SourceVersion)
	if base == "" { s.ReleaseError = "No comparable Wick release version"; return }
	// Count actual stable published releases, not arithmetic version gaps.
	// Only report an exact count once all pages have been read.
	seen := map[string]bool{}
	latest := ""
	count := 0
	for page := 1; page <= 10; page++ {
		var releases []struct {
			Tag string `json:"tag_name"`
			Draft bool `json:"draft"`
			Prerelease bool `json:"prerelease"`
		}
		if err := c.get(ctx, fmt.Sprintf("/repos/%s/releases?per_page=100&page=%d", OfficialRepo, page), &releases); err != nil {
			s.ReleaseError = "Published release comparison unavailable"
			return
		}
		for _, r := range releases {
			v := StableVersion(r.Tag)
			if r.Draft || r.Prerelease || v == "" || seen[v] { continue }
			seen[v] = true
			if latest == "" || semver.Compare(v, latest) > 0 { latest = v }
			if semver.Compare(v, base) > 0 { count++ }
		}
		if len(releases) < 100 {
			s.LatestRelease = latest
			if latest != "" { s.NewerReleases = &count } else { s.ReleaseError = "No published stable releases found" }
			return
		}
	}
	s.LatestRelease = latest
	s.ReleaseError = "Release history exceeds comparison limit; exact count unavailable"
}
