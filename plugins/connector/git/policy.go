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
	"sort"
	"strings"
)

// GlobalPolicy is layer 1 — the fallback used when no per-repo rule matches.
type GlobalPolicy struct {
	BranchPattern string

	// MessagePattern is a regex a commit message must match. Empty means unchecked —
	// most teams do not enforce one, and a default here would reject every commit on
	// a connector nobody configured for it.
	MessagePattern string

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
	Repo           string
	BranchPattern  string // regex, or "-" to clear (accept any branch name)
	MessagePattern string // regex, or "-" to clear (accept any commit message)
	Protected      string // comma-separated globs, or "-" to clear (protect nothing)
	ForcePush      string // "true" | "false" | "" or "-" (both inherit)
}

// EffectivePolicy is the single compiled policy every operation is judged
// against. Nothing downstream reads raw config — it reads this.
type EffectivePolicy struct {
	BranchPattern  string
	BranchRe       *regexp.Regexp // nil when BranchPattern is empty or invalid
	MessagePattern string
	MessageRe      *regexp.Regexp // nil when MessagePattern is empty or invalid
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
// repository's host/owner/name identity.
//
// Matching both means one rule can be written either way: "*/org/infra" targets a
// remote, "d:/code/work/*" targets a checkout location.
//
// The slug side is matched FIELD BY FIELD against a parsed identity, not as one
// string. Globbing the raw slug made every spelling of the same repository a separate
// bug: a trailing dot on the hostname ("bitbucket.org./owner/repo" — valid DNS, routes
// and TLS-verifies normally) compared unequal to the rule, so the repository fell
// through to the global fallback and every guard stopped applying. Case, ports, double
// slashes and ".git" had each been fixed one at a time; the dot was simply the variant
// nobody had thought of.
//
// Both sides are normalised through the same parser, so a rule written with a port or a
// trailing dot works too — the question becomes whether the parser agrees, which has
// one answer, instead of whether every spelling was anticipated.
func MatchRepo(glob, repoPath, repoSlug string) bool {
	if glob == "" {
		return false
	}
	g := strings.ToLower(norm(glob))

	// Path side: a path has no structure worth parsing, so it stays a plain glob.
	if repoPath != "" {
		if ok, err := path.Match(g, strings.ToLower(norm(repoPath))); err == nil && ok {
			return true
		}
	}
	if repoSlug == "" {
		return false
	}
	return matchSlug(g, repoSlug)
}

// matchSlug compares a glob against a repository identity, one field at a time.
//
// The glob is split on "/" and normalised the same way the slug is, so
// "BitBucket.org.:443/Org/Repo.git" and "bitbucket.org/org/repo" are the same rule. A
// two-segment glob matches host/owner and every repository under it, which is how
// "*/org" is meant to read.
func matchSlug(glob, slug string) bool {
	gs := splitSlugPattern(glob)
	ss := strings.Split(slug, "/")
	if len(gs) < 2 || len(ss) < 2 {
		return false
	}
	// Host and owner are single segments; a glob's "*" there must not cross a "/".
	if !segMatch(gs[0], ss[0]) || !segMatch(gs[1], ss[1]) {
		return false
	}
	// An owner-level rule covers everything under it.
	if len(gs) == 2 {
		return true
	}
	// The name can itself contain "/" (a GitLab subgroup), so the remainder of both
	// sides is compared as a whole and "*" is allowed to cross separators there.
	gName := strings.Join(gs[2:], "/")
	sName := strings.Join(ss[2:], "/")
	if strings.Contains(gName, "*") {
		return crossMatch(gName, sName)
	}
	return gName == sName
}

// splitSlugPattern splits a slug-shaped glob into normalised segments. The host
// segment goes through normHost so a rule written as "bitbucket.org.:443" matches, and
// a trailing ".git" is dropped so a rule pasted from a clone URL works.
func splitSlugPattern(glob string) []string {
	glob = strings.TrimSuffix(strings.Trim(glob, "/"), ".git")
	segs := make([]string, 0, 4)
	for _, seg := range strings.Split(glob, "/") {
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	if len(segs) > 0 {
		// Only when it is not itself a wildcard: normHost would strip nothing from "*"
		// but there is no reason to run it, and a pattern like "*." must stay intact.
		if !strings.Contains(segs[0], "*") {
			segs[0] = normHost(segs[0])
		}
	}
	return segs
}

// segMatch matches one path segment, where "*" does not cross a separator. path.Match
// already has that property, and a segment contains no separator, so it is exact.
func segMatch(pattern, value string) bool {
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}

// crossMatch matches a pattern against a value where "*" MAY cross separators, for the
// repository-name tail. path.Match refuses to let "*" span a "/", which would stop
// "*/org/*" from covering a subgroup path like "org/team/repo".
func crossMatch(pattern, value string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value
	}
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	value = value[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(value, mid)
		if i < 0 {
			return false
		}
		value = value[i+len(mid):]
	}
	return strings.HasSuffix(value, last) || last == ""
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
			Repo:           strings.TrimSpace(r["repo"]),
			BranchPattern:  strings.TrimSpace(r["branch_pattern"]),
			MessagePattern: strings.TrimSpace(r["message_pattern"]),
			Protected:      strings.TrimSpace(r["protected"]),
			ForcePush:      strings.TrimSpace(r["force_push"]),
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
		MessagePattern: g.MessagePattern,
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
		if best.MessagePattern != "" {
			if best.MessagePattern == "-" {
				p.MessagePattern = ""
			} else {
				p.MessagePattern = best.MessagePattern
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
	if p.MessagePattern != "" {
		re, err := regexp.Compile(p.MessagePattern)
		if err != nil {
			// Same fail-closed treatment as a bad branch pattern: mutations are
			// blocked until the config is fixed, reads keep working.
			p.PolicyErr = "commit message pattern does not compile: " + err.Error()
		} else {
			p.MessageRe = re
		}
	}
	return p
}

// unevaluableStricterRules returns the globs that a missing slug makes unevaluable
// AND that would have tightened the policy.
//
// Used for the warning the operations attach when a repository has no remote (see
// scopeWarning). It does NOT block: a repository with no remote is a legitimate thing
// to manage, and a slug-shaped rule usually has nothing to do with it — refusing on
// that account would deny work the policy never meant to cover. What was actually
// wrong before was the SILENCE, so this names what could not be evaluated and leaves
// the decision to the operator.
//
// Three conditions, all necessary:
//
//  1. The rule does not match the local path. If it already matches, the missing slug
//     costs nothing — the rule is being evaluated, just by the other candidate.
//  2. Its shape says it was written against a slug: at least three slash-separated
//     segments, with a dot or a wildcard in the first, because that segment is a
//     hostname. A path glob ("d:/code/*") failing to match has nothing to do with the
//     slug.
//  3. It is STRICTER than the fallback. A rule that could only have LOOSENED the
//     policy cannot make a permissive answer wrong, so warning about it would be
//     noise that trains the reader to ignore the field.
func unevaluableStricterRules(g GlobalPolicy, rules []RepoRule, repoPath string) []string {
	var out []string
	for _, r := range rules {
		if r.Repo == "" || MatchRepo(r.Repo, repoPath, "") {
			continue
		}
		segs := strings.Split(strings.Trim(norm(r.Repo), "/"), "/")
		if len(segs) < 3 {
			continue
		}
		host := segs[0]
		if !strings.Contains(host, ".") && !strings.Contains(host, "*") {
			continue
		}
		if tightensPolicy(g, r) {
			out = append(out, r.Repo)
		}
	}
	sort.Strings(out)
	return out
}

// tightensPolicy reports whether a rule would restrict more than the fallback does.
// Only such a rule turns "could not evaluate" into something worth reporting.
//
// Each column is compared against what the fallback already permits. A rule that
// clears a value ("-") or turns force push ON is a loosening, and is not reported.
func tightensPolicy(g GlobalPolicy, r RepoRule) bool {
	// A pattern where the fallback has none is new enforcement. A DIFFERENT pattern
	// counts too: neither is a subset of the other in general, so it may refuse names
	// the fallback accepts.
	if p := r.BranchPattern; p != "" && p != "-" && p != g.BranchPattern {
		return true
	}
	if p := r.MessagePattern; p != "" && p != "-" && p != g.MessagePattern {
		return true
	}
	// A protected list is stricter when it names anything the fallback does not.
	if r.Protected != "" && r.Protected != "-" {
		for _, b := range splitCSV(r.Protected) {
			if !containsFold(g.Protected, b) {
				return true
			}
		}
	}
	// Denying force push where the fallback allows it is stricter; allowing it where
	// the fallback denies is a loosening.
	if r.ForcePush == "false" && g.AllowForcePush {
		return true
	}
	return false
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
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
	Message       string
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
			// Name the operation being refused, not the one the config is named after.
			// "force push is not allowed" in answer to a reset mentions a push that is not
			// happening, which reads as the connector having misunderstood the request.
			return deny(forceDenyReason(r.Op))
		}
		if IsProtected(p, r.Branch) {
			return deny(fmt.Sprintf("branch %q is protected; direct %s is blocked", r.Branch, r.Op))
		}
		if r.NewBranch && p.BranchRe != nil && !p.BranchRe.MatchString(r.Branch) {
			return deny(fmt.Sprintf("branch %q does not match the required pattern %s",
				r.Branch, p.BranchPattern))
		}
		// Only a commit carries a message, so only a commit is judged on one. An
		// empty message is left to git, which refuses it with its own error.
		if r.Op == "commit" && r.Message != "" && p.MessageRe != nil &&
			!p.MessageRe.MatchString(r.Message) {
			return deny(fmt.Sprintf("commit message does not match the required pattern %s",
				p.MessagePattern))
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

// forceDenyReason names the operation actually being refused.
//
// allow_force_push gates two different things — a force push and a hard reset — so a
// single message written from the config's name misdescribed one of them. Answering a
// "reset --hard" with "force push is not allowed" mentions an operation that is not
// happening, and the natural reading is that the connector misunderstood the request
// rather than that a policy applied.
//
// The config key is still named after the push, and is still cited, because that is
// what an operator has to go and change. Renaming it to something like
// allow_history_rewrite would describe both halves better, but the key is stored per
// instance and a rename would silently reset every configured value to false — a
// permissive-to-restrictive flip that would look like the connector had broken.
func forceDenyReason(op string) string {
	what := "force push"
	if op == "reset" {
		what = "hard reset"
	}
	return what + " is not allowed by this policy (allow_force_push is off, which gates " +
		"both force push and hard reset)"
}

// scopeWarning describes a policy that was resolved without a repository slug, or ""
// when there is nothing worth saying.
//
// This is the reportable half of the "repo with no remote" hole. Resolution falls back
// to the global policy, which may be far more permissive than the per-repo rules an
// operator wrote — and the original complaint was not that this happens, it is that it
// happened silently. Every operation attaches this to its response, so a permissive
// verdict arrives with the reason it might be wrong.
func scopeWarning(g GlobalPolicy, rules []RepoRule, repoPath, repoSlug string) string {
	if repoSlug != "" {
		return ""
	}
	globs := unevaluableStricterRules(g, rules, repoPath)
	if len(globs) == 0 {
		return ""
	}
	return "this repository has no readable remote, so host/owner/repo is unknown and " +
		"these stricter rules could not be evaluated: " + strings.Join(globs, ", ") +
		". The global fallback was used instead, which may be more permissive"
}
