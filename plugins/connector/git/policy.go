// Package main implements the git CLI connector plugin.
//
// policy.go holds the policy engine: it turns the two config layers (global
// fields + per-repo rows) into a single EffectivePolicy, then evaluates an
// operation against it. Everything here is pure Go — no I/O, no git — so the
// whole engine is unit-testable without git installed.
package main

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// GlobalPolicy is layer 1 — the fallback used when no per-repo rule matches.
type GlobalPolicy struct {
	BranchPattern  string
	Protected      []string
	AllowForcePush bool
	RawEnabled     bool
	RawRules       map[string]string // subcommand → "allow" | "deny"
}

// RepoRule is one layer-2 row from the repo_policies kvlist. An empty column
// inherits from GlobalPolicy.
//
// "-" clears the inherited value, but only where "no value" is a meaningful
// state: the pattern and list columns. ForcePush is a boolean with no third
// state, so "-" there means the same as empty — inherit.
type RepoRule struct {
	Repo          string
	BranchPattern string // regex, or "-" to clear (accept any branch name)
	Protected     string // comma-separated globs, or "-" to clear (protect nothing)
	ForcePush     string // "true" | "false" | "" or "-" (both inherit)
}

// EffectivePolicy is the single compiled policy every operation is judged
// against. Nothing downstream reads raw config — it reads this.
type EffectivePolicy struct {
	BranchPattern  string
	BranchRe       *regexp.Regexp // nil when BranchPattern is empty or invalid
	Protected      []string
	AllowForcePush bool
	RawEnabled     bool
	RawRules       map[string]string
	MatchedRule    string // "global" or "per-repo → <glob>"; never empty
	PolicyErr      string // non-empty when config is malformed → deny mutations
}

// ParseKVList decodes a kvlist config value. The manager stores kvlist fields as
// a JSON array of string-keyed objects; an unset field is the empty string.
func ParseKVList(s string) ([]map[string]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil, nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(s), &rows); err != nil {
		return nil, fmt.Errorf("parse kvlist: %w", err)
	}
	return rows, nil
}

// Specificity counts wildcards in a repo glob. Fewer wildcards = more specific,
// so "*/org/sandbox" (1) beats "*/org/*" (2). Used to order competing rules
// deterministically instead of relying on the order rows were written.
func Specificity(glob string) int {
	return strings.Count(glob, "*")
}

// MatchRepo reports whether a repo glob matches either the local path or the
// host/owner/repo slug. Matching both means one rule can be written either way:
// "*/org/infra" targets a remote, "d:/code/work/*" targets a checkout location.
//
// Separators are normalised to "/" and both sides lowercased so Windows paths
// (d:\code\Work) match a d:/code/* glob written by hand.
func MatchRepo(glob, repoPath, repoSlug string) bool {
	if glob == "" {
		return false
	}
	g := strings.ToLower(norm(glob))
	for _, candidate := range []string{repoPath, repoSlug} {
		if candidate == "" {
			continue
		}
		if ok, err := path.Match(g, strings.ToLower(norm(candidate))); err == nil && ok {
			return true
		}
	}
	return false
}

// norm converts Windows separators to forward slashes so one glob syntax works
// on every platform.
func norm(s string) string { return strings.ReplaceAll(s, `\`, "/") }

// ParseRepoRules decodes the repo_policies kvlist into typed rules.
func ParseRepoRules(s string) ([]RepoRule, error) {
	rows, err := ParseKVList(s)
	if err != nil {
		return nil, err
	}
	out := make([]RepoRule, 0, len(rows))
	for _, r := range rows {
		rule := RepoRule{
			Repo:          strings.TrimSpace(r["repo"]),
			BranchPattern: strings.TrimSpace(r["branch_pattern"]),
			Protected:     strings.TrimSpace(r["protected"]),
			ForcePush:     strings.TrimSpace(r["force_push"]),
		}
		if rule.Repo == "" {
			continue // a row with no repo glob can never match; drop it
		}
		out = append(out, rule)
	}
	return out, nil
}

// Resolve compiles the two layers into one EffectivePolicy for a specific repo.
//
// Layer 2 wins over layer 1, but only for the single best-matching rule: the
// fewest wildcards wins, and a tie goes to the stricter rule so the result never
// depends on the order rows were written.
func Resolve(g GlobalPolicy, rules []RepoRule, repoPath, repoSlug string) EffectivePolicy {
	p := EffectivePolicy{
		BranchPattern:  g.BranchPattern,
		Protected:      append([]string(nil), g.Protected...),
		AllowForcePush: g.AllowForcePush,
		RawEnabled:     g.RawEnabled,
		RawRules:       g.RawRules,
		MatchedRule:    "global",
	}

	if best, ok := bestRule(rules, repoPath, repoSlug); ok {
		p.MatchedRule = "per-repo → " + best.Repo
		if best.BranchPattern != "" {
			if best.BranchPattern == "-" {
				p.BranchPattern = ""
			} else {
				p.BranchPattern = best.BranchPattern
			}
		}
		switch best.Protected {
		case "": // inherit
		case "-":
			p.Protected = nil
		default:
			p.Protected = splitCSV(best.Protected)
		}
		switch best.ForcePush {
		case "true":
			p.AllowForcePush = true
		case "false":
			p.AllowForcePush = false
		} // "" and "-" inherit
	}

	if p.BranchPattern != "" {
		re, err := regexp.Compile(p.BranchPattern)
		if err != nil {
			p.PolicyErr = "branch pattern does not compile: " + err.Error()
		} else {
			p.BranchRe = re
		}
	}
	return p
}

// bestRule picks the winning layer-2 rule: fewest wildcards, then stricter.
func bestRule(rules []RepoRule, repoPath, repoSlug string) (RepoRule, bool) {
	var best RepoRule
	found := false
	for _, r := range rules {
		if !MatchRepo(r.Repo, repoPath, repoSlug) {
			continue
		}
		if !found {
			best, found = r, true
			continue
		}
		switch {
		case Specificity(r.Repo) < Specificity(best.Repo):
			best = r
		case Specificity(r.Repo) == Specificity(best.Repo) && stricter(r, best):
			best = r
		}
	}
	return best, found
}

// stricter reports whether a is more restrictive than b. Used only to break a
// specificity tie deterministically; denying force push and protecting more
// branches both count as stricter.
func stricter(a, b RepoRule) bool {
	if a.ForcePush != b.ForcePush {
		return a.ForcePush == "false"
	}
	if a.Protected == "-" || b.Protected == "-" {
		return b.Protected == "-"
	}
	if na, nb := len(splitCSV(a.Protected)), len(splitCSV(b.Protected)); na != nb {
		return na > nb
	}
	// Strictness cannot separate these two rules. Fall back to lexicographic
	// order on the glob so the winner is still deterministic — otherwise the
	// result would depend on the order the rows happen to sit in the config,
	// and reordering the table would silently change which policy applies.
	return a.Repo < b.Repo
}

// Request is one operation being judged. Branch is the target branch for a push
// or the current branch for a commit; NewBranch marks branch creation so the
// name pattern applies.
type Request struct {
	Op            string
	Branch        string
	Remote        string
	Force         bool
	NewBranch     bool
	RawSubcommand string
}

// Verdict is the policy outcome. A denied verdict always carries a Reason, and
// MatchedRule always names the layer that decided, so the manager UI and the
// operation response can both explain themselves.
type Verdict struct {
	Allow       bool
	Reason      string
	MatchedRule string
}

// mutatingOps are the operations that change repository or remote state. Only
// these are blocked when the policy config is malformed — reads stay available
// so an admin can still inspect the repo while fixing the config.
//
// "raw" is listed for the malformed-config guard above, which runs before raw is
// routed to evaluateRaw. It is deliberately never reached by the branch-protection
// path: a raw subcommand's target branch is not knowable from its arguments, so
// raw is gated by its allow list instead.
var mutatingOps = map[string]bool{
	"push": true, "commit": true, "merge": true, "reset": true, "rebase": true,
	"branch_create": true, "checkout": true, "add": true, "stash": true,
	"stash_drop": true, "tag": true, "tag_delete": true, "pull": true,
	"clone": true, "raw": true,
}

// Evaluate judges a request against the compiled policy. It never touches the
// filesystem or spawns a process, so a denial costs nothing.
func (p EffectivePolicy) Evaluate(r Request) Verdict {
	deny := func(reason string) Verdict {
		return Verdict{Allow: false, Reason: reason, MatchedRule: p.MatchedRule}
	}
	allow := func() Verdict {
		return Verdict{Allow: true, MatchedRule: p.MatchedRule}
	}

	// Malformed config blocks mutations and only mutations.
	if p.PolicyErr != "" {
		if mutatingOps[r.Op] {
			return deny("policy config is invalid, mutating operations are blocked: " + p.PolicyErr)
		}
		return allow()
	}

	if r.Op == "raw" {
		return p.evaluateRaw(r, deny, allow)
	}

	// Branch and force rules apply to mutations only — reading a protected branch,
	// or diffing one, changes nothing. Keeping the force check inside this gate
	// matters because the service layer fills Request generically: a read op that
	// happens to carry Force would otherwise be denied as a force push.
	if mutatingOps[r.Op] {
		if r.Force && !p.AllowForcePush {
			return deny("force push is not allowed by this policy (allow_force_push is off)")
		}
		if IsProtected(p, r.Branch) {
			return deny(fmt.Sprintf("branch %q is protected; direct %s is blocked", r.Branch, r.Op))
		}
		if r.NewBranch && p.BranchRe != nil && !p.BranchRe.MatchString(r.Branch) {
			return deny(fmt.Sprintf("branch %q does not match the required pattern %s",
				r.Branch, p.BranchPattern))
		}
	}
	return allow()
}

// evaluateRaw gates the raw operation. Both the master switch and an explicit
// per-subcommand allow are required; an unlisted subcommand is denied.
func (p EffectivePolicy) evaluateRaw(r Request, deny func(string) Verdict, allow func() Verdict) Verdict {
	if !p.RawEnabled {
		return deny("the raw operation is disabled for this connector")
	}
	sub := strings.ToLower(strings.TrimSpace(r.RawSubcommand))
	if sub == "" {
		return deny("no git subcommand could be determined from the arguments")
	}
	switch p.RawRules[sub] {
	case "deny":
		return deny(fmt.Sprintf("subcommand %q is explicitly denied", sub))
	case "allow":
		return allow()
	default:
		return deny(fmt.Sprintf("subcommand %q is not in the allow list", sub))
	}
}

// IsProtected reports whether a branch matches any protected glob. Comparison
// is case-insensitive because git branch names are case-sensitive on Linux but
// not on Windows/macOS checkouts — treating "Master" as unprotected would be a
// trivial bypass.
func IsProtected(p EffectivePolicy, branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	if b == "" {
		return false
	}
	for _, glob := range p.Protected {
		if ok, err := path.Match(strings.ToLower(glob), b); err == nil && ok {
			return true
		}
	}
	return false
}

// regexpCompile wraps regexp.Compile so callers that only need validation do not
// import regexp themselves.
func regexpCompile(pattern string) (*regexp.Regexp, error) { return regexp.Compile(pattern) }

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
