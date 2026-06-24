package updater

import (
	"strings"
	"testing"
)

const sampleChangelog = `# Changelog

---

## [Unreleased]

_Nothing yet._

---

## [v0.23.6](https://github.com/yogasw/wick/compare/v0.23.5...v0.23.6) — Self-Update

_Released on 2026-06-24_

### Added
*   Self-update System page.

---

## [v0.23.5](https://github.com/yogasw/wick/compare/v0.23.4...v0.23.5) — Updater

_Released on 2026-06-24_

### Fixed
- Termux self-update.

---

## [v0.23.4](https://github.com/yogasw/wick/compare/v0.23.3...v0.23.4) — Self-Update

_Released on 2026-06-24_

### Added
- More stuff.

---

## [v0.23.3](https://github.com/yogasw/wick/compare/v0.23.2...v0.23.3)

_Released on 2026-06-23_

### Fixed
- Old fix.
`

func TestExtractChangelogRange_SpansVersions(t *testing.T) {
	got := extractChangelogRange(sampleChangelog, "0.23.4", "0.23.6")

	// Range (0.23.4, 0.23.6] → v0.23.6 + v0.23.5 sections, NOT v0.23.4
	// (exclusive lower bound), NOT v0.23.3 (below range), NOT Unreleased.
	// Assert on the section CONTENT (unique body lines) rather than the
	// version strings — version numbers also appear inside compare URLs
	// of adjacent headings, which would give false positives.
	for _, want := range []string{"Self-update System page", "Termux self-update"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected range to contain %q\ngot:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"Unreleased", "More stuff", "Old fix"} {
		if strings.Contains(got, notWant) {
			t.Errorf("range should NOT contain %q\ngot:\n%s", notWant, got)
		}
	}
	// The v0.23.4 / v0.23.3 section HEADINGS must be absent (their compare
	// URLs may still mention those tags, so match the heading form).
	for _, notWant := range []string{"] — Self-Update\n\n_Released on 2026-06-24_\n\n### Added\n- More stuff"} {
		if strings.Contains(got, notWant) {
			t.Errorf("range should NOT contain the v0.23.4 section\ngot:\n%s", got)
		}
	}
	// Newest first: v0.23.6 appears before v0.23.5.
	if strings.Index(got, "v0.23.6") > strings.Index(got, "v0.23.5") {
		t.Errorf("expected newest-first order (v0.23.6 before v0.23.5)\ngot:\n%s", got)
	}
}

func TestExtractChangelogRange_DevBuildTakesAllUpToLatest(t *testing.T) {
	// current "" (dev/unknown) → everything up to and including latest.
	got := extractChangelogRange(sampleChangelog, "", "0.23.5")
	if !strings.Contains(got, "v0.23.5") || !strings.Contains(got, "v0.23.3") {
		t.Errorf("dev build should include all <= latest\ngot:\n%s", got)
	}
	if strings.Contains(got, "v0.23.6") {
		t.Errorf("should not include versions above latest (v0.23.6)\ngot:\n%s", got)
	}
}

func TestExtractChangelogRange_UpToDateEmpty(t *testing.T) {
	// current == latest → nothing newer, empty result.
	if got := extractChangelogRange(sampleChangelog, "0.23.6", "0.23.6"); got != "" {
		t.Errorf("expected empty when already latest, got:\n%s", got)
	}
}
