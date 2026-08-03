package delegation

import (
	"strings"
	"testing"
)

// The prompt says "no credentials". This is what makes it true even when
// an injection persuades the drafter otherwise.
func TestSanitizeMasksAKnownSecret(t *testing.T) {
	draft := "We reset the integration using token sk-live-abcdef123456 and the issue cleared."
	got, masked := SanitizeClientDraft(draft, []string{"sk-live-abcdef123456"}, "")

	if !masked {
		t.Fatal("a known secret was not reported as masked")
	}
	if strings.Contains(got, "sk-live-abcdef123456") {
		t.Fatalf("the secret survived: %q", got)
	}
	if !strings.Contains(got, maskToken) {
		t.Fatalf("nothing was substituted: %q", got)
	}
}

// A clean draft must come back untouched, and unflagged — a warning on
// every draft is a warning nobody reads.
func TestSanitizeLeavesACleanDraftAlone(t *testing.T) {
	draft := "Webhook deliveries to abc.com failed between 10:00 and 11:00. They are working now."
	got, masked := SanitizeClientDraft(draft, []string{"sk-live-abcdef123456"}, "D:/code/work/abc")

	if masked {
		t.Fatal("a clean draft was flagged as masked")
	}
	if got != draft {
		t.Fatalf("a clean draft was rewritten:\n%q\n%q", got, draft)
	}
}

// A customer has no use for a directory layout, and it identifies
// internal structure precisely.
func TestSanitizeReducesProjectPathsToTheirFilename(t *testing.T) {
	cases := []struct {
		name, draft, root, want string
	}{
		{
			name:  "forward slashes",
			draft: "The error came from D:/code/work/abc/internal/webhook/sign.go during retry.",
			root:  "D:/code/work/abc",
			want:  "sign.go",
		},
		{
			name:  "backslashes, as a Windows log writes them",
			draft: `The error came from D:\code\work\abc\internal\webhook\sign.go during retry.`,
			root:  `D:\code\work\abc`,
			want:  "sign.go",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, masked := SanitizeClientDraft(tc.draft, nil, tc.root)
			if !masked {
				t.Fatal("a project path was not reported as masked")
			}
			if strings.Contains(got, "internal") {
				t.Fatalf("the directory layout survived: %q", got)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("the filename was lost: %q", got)
			}
		})
	}
}

// A two-character "secret" appears inside ordinary words, and a draft
// with fragments of every third word blanked is worse than one that
// leaked nothing anyway.
func TestSanitizeIgnoresValuesTooShortToMaskSafely(t *testing.T) {
	draft := "The API returned an error to the customer."
	got, masked := SanitizeClientDraft(draft, []string{"an", "API"}, "")

	if masked {
		t.Fatalf("a short value was masked: %q", got)
	}
	if got != draft {
		t.Fatalf("draft mangled: %q", got)
	}
}

// Every occurrence, not just the first: a draft that mentions a token
// twice must not leak it the second time.
func TestSanitizeMasksEveryOccurrence(t *testing.T) {
	draft := "Token sk-live-abcdef123456 was rotated; sk-live-abcdef123456 is no longer valid."
	got, _ := SanitizeClientDraft(draft, []string{"sk-live-abcdef123456"}, "")

	if strings.Contains(got, "sk-live-abcdef123456") {
		t.Fatalf("a later occurrence survived: %q", got)
	}
}
