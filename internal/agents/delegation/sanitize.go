package delegation

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// Client-draft sanitising.
//
// The drafter's prompt tells it not to include logs, stack traces or
// credentials. That is guidance, and guidance is not a control: a prompt
// injection that convinces the drafter to quote a token produces a draft
// containing a token. This runs on the way out, so the same injection
// produces a MASKED token instead.
//
// Deliberately dumb — literal value replacement, no pattern matching. A
// regex that tries to recognise "a secret" invents both false positives
// (mangling the customer's own order number) and false negatives (missing
// the format nobody anticipated). Values wick already knows are secret
// are the ones it can mask honestly.

// ClientDrafterRoleKey is the role whose output is customer-facing, and
// therefore the one that gets sanitised.
const ClientDrafterRoleKey = "client-response-drafter"

// maskToken is what replaces a masked value.
const maskToken = "***"

// DraftMinSecretLen is the shortest value worth masking.
//
// Below this, replacement does more harm than good: a two-character
// "secret" appears inside ordinary words, and a draft with fragments of
// every third word blanked is worse than one that leaked nothing anyway.
const DraftMinSecretLen = 6

// SanitizeClientDraft masks values that must never reach a customer and
// reports whether anything was masked.
//
// projectRoot, when set, reduces absolute paths beneath it to their base
// name: a customer has no use for a directory layout, and it is exactly
// the kind of detail that identifies internal structure.
func SanitizeClientDraft(draft string, secrets []string, projectRoot string) (string, bool) {
	masked := false

	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if len(s) < DraftMinSecretLen || !strings.Contains(draft, s) {
			continue
		}
		draft = strings.ReplaceAll(draft, s, maskToken)
		masked = true
	}

	if out, hit := stripProjectPaths(draft, projectRoot); hit {
		draft, masked = out, true
	}
	return draft, masked
}

// stripProjectPaths reduces absolute paths under root to their base name.
//
// Both separators are handled: a Windows host writes backslashes into
// logs that a sub-agent then quotes verbatim.
func stripProjectPaths(draft, root string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return draft, false
	}
	variants := []string{filepath.Clean(root)}
	if alt := strings.ReplaceAll(variants[0], "\\", "/"); alt != variants[0] {
		variants = append(variants, alt)
	}

	hit := false
	for _, prefix := range variants {
		for {
			i := strings.Index(draft, prefix)
			if i < 0 {
				break
			}
			end := i + len(prefix)
			// Consume the rest of the path: everything up to whitespace or
			// a character that cannot appear in one.
			for end < len(draft) && !isPathBreak(rune(draft[end])) {
				end++
			}
			full := draft[i:end]
			replacement := filepath.Base(strings.ReplaceAll(full, "\\", "/"))
			if replacement == "" || replacement == "." {
				replacement = maskToken
			}
			draft = draft[:i] + replacement + draft[end:]
			hit = true
		}
	}
	return draft, hit
}

func isPathBreak(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '"', '\'', ')', ']', '}', ',', ';':
		return true
	}
	return false
}

// sanitizeDraft applies the service's configured secret and path sources
// to one draft.
func (s *Service) sanitizeDraft(ctx context.Context, row *entity.AgentDelegation, draft string) (string, bool) {
	if strings.TrimSpace(draft) == "" {
		return draft, false
	}
	var secrets []string
	if s.Secrets != nil {
		secrets = s.Secrets(ctx, row.ProjectID)
	}
	root := ""
	if s.ProjectRoot != nil {
		root = s.ProjectRoot(ctx, row.ProjectID)
	}
	return SanitizeClientDraft(draft, secrets, root)
}

// SanitizeNote is appended to a masked draft's result, so a human knows
// to look before sending rather than discovering *** in front of the
// customer.
const SanitizeNote = "Sensitive values were masked in this draft. Review it before sending."
