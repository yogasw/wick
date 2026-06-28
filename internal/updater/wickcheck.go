package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// wickRepoOwner/wickRepoName are the canonical public wick framework
// repo. The framework version embedded in any build (downstream apps
// included) can always be checked against this repo, regardless of
// whether THIS app set its own release source — the framework is open
// source and needs no PAT. This is the same repo cmd/cli/upgrade uses.
const (
	wickRepoOwner = "yogasw"
	wickRepoName  = "wick"
)

// WickVersionStatus is the result of checking the embedded wick framework
// version against the public wick repo's latest release. It powers the
// version card's "Latest / Update available" badge and "What's new" block
// even when the app's own updater is not configured.
type WickVersionStatus struct {
	Current      string `json:"current"`       // embedded wick version (vX.Y.Z), "" if dev/unknown
	Latest       string `json:"latest"`        // latest wick release tag (vX.Y.Z)
	UpToDate     bool   `json:"up_to_date"`    // Current >= Latest
	ReleaseNotes string `json:"release_notes"` // changelog range Current→Latest (markdown)
	PublishedAt  string `json:"published_at"`  // latest release date (RFC3339)
	ChangelogURL string `json:"changelog_url"` // link to the full changelog site
}

// CheckWickVersion looks up the latest wick framework release on the
// public wick repo and compares it to current (the embedded wick
// version). It does NOT touch the app's configured updater or any PAT —
// the wick repo is public — so it works on a build with no release
// source set. On a network error it returns the error; callers should
// degrade gracefully (show the version plain, no badge).
func CheckWickVersion(ctx context.Context, current string) (WickVersionStatus, error) {
	cur := normalizeVer(current)
	st := WickVersionStatus{
		Current:      cur,
		ChangelogURL: officialChangelogPageURL,
	}

	tag, publishedAt, err := fetchWickLatestRelease(ctx)
	if err != nil {
		return st, err
	}
	latest := normalizeVer(tag)
	st.Latest = latest
	st.PublishedAt = publishedAt
	st.UpToDate = !semverNewer(latest, cur)

	// What changed between the running framework version and latest —
	// pulled from the public changelog site (same source the official
	// update card uses). The range is (current, latest]; on an up-to-date
	// build it's empty, which is correct (nothing new to show).
	if body, ferr := fetchText(ctx, officialChangelogMarkdownURL); ferr == nil {
		st.ReleaseNotes = extractChangelogRange(body, cur, latest)
	}
	return st, nil
}

// fetchWickLatestRelease GETs the latest release tag + publish date from
// the public wick repo. No PAT: the repo is public, and the System page
// check must work on builds with no release source configured.
func fetchWickLatestRelease(ctx context.Context) (tag, publishedAt string, err error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPI, wickRepoOwner, wickRepoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := newClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if isRateLimited(resp) {
			return "", "", fmt.Errorf("github releases/latest: rate limit (%d): %s — unauthenticated API is capped at 60/hr per IP; retry after %s",
				resp.StatusCode, githubMessage(body), rateLimitResetHint(resp))
		}
		return "", "", fmt.Errorf("github releases/latest: %d: %s", resp.StatusCode, githubMessage(body))
	}
	var info struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", err
	}
	if info.TagName == "" {
		return "", "", fmt.Errorf("github releases/latest: empty tag")
	}
	return info.TagName, info.PublishedAt, nil
}
