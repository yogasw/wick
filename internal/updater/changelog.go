package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// officialChangelogMarkdownURL is the RAW markdown the updater FETCHES
// and parses for the changelog range. For the official wick-agent
// (release repo yogasw/wick), app + framework share one codebase + one
// changelog, so the System page sources "what changed" here rather than
// the GitHub release body. Downstream apps fall back to the release body.
const officialChangelogMarkdownURL = "https://yogasw.github.io/wick/changelog.md"

// officialChangelogPageURL is the rendered HTML page a USER opens via the
// "View full changelog" link — the VitePress site serves .html, not raw
// .md (the .md would render as plain text in a browser).
const officialChangelogPageURL = "https://yogasw.github.io/wick/changelog.html"

// isOfficial reports whether this binary's release repo is the canonical
// wick repo. Only then do we trust the public changelog site.
func (u *Updater) isOfficial() bool {
	return strings.EqualFold(u.owner, "yogasw") && strings.EqualFold(u.repo, "wick")
}

// IsOfficial is the exported form of isOfficial — the System page uses
// it to decide whether the wick framework version moves with the app
// (official build) or is just bundled (downstream app).
func (u *Updater) IsOfficial() bool { return u.isOfficial() }

// ChangelogURL is where a human can read the full changelog: the
// published site for the official build, otherwise the release repo's
// GitHub releases page. Empty when the updater isn't configured.
func (u *Updater) ChangelogURL() string {
	if u.isOfficial() {
		return officialChangelogPageURL
	}
	if u.owner == "" || u.repo == "" {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s/releases", u.owner, u.repo)
}

// changelogVersionHeading matches a changelog section heading like:
//
//	## [v0.23.6](https://github.com/yogasw/wick/compare/...) — Self-Update
//	## [Unreleased]
//
// Capture group 1 is the bracketed label (e.g. "v0.23.6" or "Unreleased").
var changelogVersionHeading = regexp.MustCompile(`(?m)^##\s+\[([^\]]+)\]`)

// ChangelogRange fetches the official changelog and returns the
// concatenated entries for every released version newer than `current`
// up to and including `latest` — i.e. everything the user gains by
// updating. Headings are kept so the UI can show "v0.23.6", "v0.23.5", …
// in order (newest first, matching the file). Returns "" (no error) when
// not the official build, when the fetch fails, or when nothing matches —
// callers then fall back to the GitHub release body.
//
// current/latest are version tags (with or without leading "v"). The
// range is (current, latest]: strictly newer than current, up to latest.
func (u *Updater) ChangelogRange(ctx context.Context, current, latest string) string {
	if !u.isOfficial() {
		return ""
	}
	body, err := fetchText(ctx, officialChangelogMarkdownURL)
	if err != nil || body == "" {
		return ""
	}
	return extractChangelogRange(body, current, latest)
}

// extractChangelogRange is the pure-parsing core of ChangelogRange,
// split out so it's unit-testable without a network fetch. It walks the
// markdown, slicing it into per-version sections by the `## [vX]`
// headings, and keeps the sections whose version is in (current, latest].
func extractChangelogRange(markdown, current, latest string) string {
	cur := normalizeVer(current)
	lat := normalizeVer(latest)

	locs := changelogVersionHeading.FindAllStringSubmatchIndex(markdown, -1)
	if len(locs) == 0 {
		return ""
	}

	var out []string
	for i, loc := range locs {
		label := markdown[loc[2]:loc[3]] // capture group 1
		ver := normalizeVer(label)
		if !semver.IsValid(ver) {
			continue // skip "Unreleased" and any non-version heading
		}
		// Section spans from this heading to the next heading (or EOF).
		start := loc[0]
		end := len(markdown)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		// Keep when current < ver <= latest. When current is empty
		// (dev/unknown build) treat every version up to latest as "new".
		newerThanCurrent := cur == "" || semver.Compare(ver, cur) > 0
		atMostLatest := lat == "" || semver.Compare(ver, lat) <= 0
		if newerThanCurrent && atMostLatest {
			out = append(out, strings.TrimRight(markdown[start:end], "\n"))
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n")
}

// fetchText GETs url and returns the body as a string. Small helper for
// the changelog fetch; bounded read so a huge/hostile response can't
// blow memory.
func fetchText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := newClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("changelog %d", resp.StatusCode)
	}
	const maxChangelog = 1 << 20 // 1 MiB is plenty for a changelog
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxChangelog))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
