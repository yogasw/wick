package main

import (
	"strings"
	"testing"
)

func TestParseDenyPatterns(t *testing.T) {
	got := ParseDenyPatterns("# notes\nacme-corp\n\n  \\bzeta\\b  \n# more\n")
	if len(got) != 2 || got[0] != "acme-corp" || got[1] != `\bzeta\b` {
		t.Fatalf("patterns = %q", got)
	}
}

// The sensitive-data guard refuses a commit whose message or added lines
// carry a listed word, case-insensitively, and lets a clean one through.
func TestEvaluateCommitDenyPatterns(t *testing.T) {
	p := Resolve(GlobalPolicy{DenyPatterns: []string{"acme-corp", `\bzeta\b`}}, nil, "/r/api", "github.com/org/api")
	if p.PolicyErr != "" {
		t.Fatalf("PolicyErr = %q", p.PolicyErr)
	}
	for _, tc := range []struct {
		name  string
		req   Request
		where string
	}{
		{"message", Request{Op: "commit", Branch: "dev", Message: "fix for ACME-Corp thread"}, "commit message"},
		{"added line", Request{Op: "commit", Branch: "dev", Message: "improve tests", Added: "name := \"Zeta\"\n"}, "staged changes"},
	} {
		v := p.Evaluate(tc.req)
		if v.Allow || !strings.Contains(v.Reason, tc.where) || !strings.Contains(v.Reason, "sensitive-data") {
			t.Errorf("%s: verdict %+v, want a refusal naming %q", tc.name, v, tc.where)
		}
	}
	clean := Request{Op: "commit", Branch: "dev", Message: "improve tests", Added: "name := \"client A\"\nzetabyte\n"}
	if v := p.Evaluate(clean); !v.Allow {
		t.Errorf("clean commit refused: %s", v.Reason)
	}
	// Reads are never judged on content.
	if v := p.Evaluate(Request{Op: "status", Message: "acme-corp"}); !v.Allow {
		t.Errorf("read refused: %s", v.Reason)
	}
}

func TestDenyPatternThatDoesNotCompileFailsClosed(t *testing.T) {
	p := Resolve(GlobalPolicy{DenyPatterns: []string{"(broken"}}, nil, "/r/api", "github.com/org/api")
	if p.PolicyErr == "" || !strings.Contains(p.PolicyErr, "sensitive-data") {
		t.Fatalf("PolicyErr = %q", p.PolicyErr)
	}
	if v := p.Evaluate(Request{Op: "commit", Branch: "dev", Message: "x"}); v.Allow {
		t.Fatal("commit allowed despite a malformed deny pattern")
	}
}
