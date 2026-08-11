// policyshow.go implements policy_show: the one policy operation an agent can
// call.
//
// Why it exists. Before this, the only way an agent learned a rule was to violate
// it: Evaluate's deny reason carries the pattern, so a failed branch_create teaches
// the branch pattern. That works, and it is how the connector was actually used,
// but it degrades in three ways that no amount of prompt wording fixes:
//
//   - Evaluate returns at the FIRST denial, so a request that breaks two rules
//     reveals the second only after the first is fixed. Fixing by trial costs one
//     round trip per rule.
//   - A rule that has not been violated yet is invisible. An agent cannot know a
//     commit message pattern exists until a commit is refused.
//   - The reason prints a value without naming its language. "master, main,
//     release/*" is a glob list and "^ai/.+$" is a regex, and nothing says which is
//     which — so a glob written into a pattern field compiles as a regex and then
//     matches almost nothing.
//
// So this op answers the question directly, and answers it PER REPOSITORY, because
// resolution is per repository: the same connector gives different rules for
// different paths, and a policy reported without a repo would be a guess.
//
// It spawns no process and reads only config plus the repo's remote URL, so it is
// a read operation in the strict sense and safe to call before anything else.
package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// resolveRepoIdentity turns one operator-supplied string into the (path, slug) pair
// MatchRepo needs, and reports which kind of input it was.
//
// MatchRepo tries a glob against BOTH the local path and host/owner/repo, so a rule
// can be written either way and this has to fill in whichever the input supplies.
// The two are not interchangeable: a rule written as "*/org/api" matches the slug,
// one written as "d:/code/*" matches the path, and reporting the wrong one silently
// returns the fallback policy as though no override existed.
func resolveRepoIdentity(c *connector.Ctx, raw, remote string) (id repoIdentity, err error) {
	// A URL first: it is unambiguous, and it is the case a path cannot cover because
	// the repository need not exist yet.
	if looksLikeRemoteURL(raw) {
		slug := RepoSlug(repoURLOf(raw))
		if slug == "" {
			return id, fmt.Errorf("could not read host/owner/repo out of %q — pass a clone URL such as https://host/owner/repo.git", raw)
		}
		return repoIdentity{Slug: slug, Kind: "url"}, nil
	}

	// host/owner/repo written bare, with no scheme. Accepted because it is what the
	// policy rules themselves are written in, so an operator reading a rule can paste
	// it straight back in.
	if isBareSlug(raw) {
		return repoIdentity{Slug: strings.TrimSuffix(raw, ".git"), Kind: "slug"}, nil
	}

	// Otherwise a local path. Validated, because a typo here would silently resolve
	// to the fallback policy and read as "this repo has no rules".
	if err := validateRepo(c, raw); err != nil {
		return id, err
	}
	// The slug comes from the remote. A remote that does not exist is an ERROR, not a
	// missing slug, and this is the one place where that distinction matters most.
	//
	// Reporting the fallback policy instead is the worst possible answer from this
	// operation. Its whole promise is "ask before you act", so an agent that asks with
	// a mistyped remote is told the repository has no branch pattern, no protected
	// branches and no message rule — a policy that does not exist, delivered by the
	// operation whose job was to prevent guessing. Enforcement is unaffected (the real
	// operations refuse an unknown remote outright), which is exactly what makes it
	// dangerous: nothing downstream corrects the lie.
	// repoScope makes the distinction that matters here, and makes it the SAME way the
	// operations do: a remote that does not exist is an error, a repository with no
	// remote at all resolves under the fallback with a warning. Sharing the helper is
	// the point — this operation exists to predict what the operations will do, so a
	// second copy of the rule could disagree with them.
	named := firstNonEmpty(remote, "origin")
	slug, warn, err := repoScope(c, raw, named)
	if err != nil {
		return id, err
	}
	if slug == "" {
		return repoIdentity{Path: raw, Kind: "path", Warning: warn}, nil
	}
	return repoIdentity{Path: raw, Slug: slug, Kind: "path", Remote: named,
		RemoteURL: remoteURL(c, raw, named)}, nil
}

// repoIdentity is what one identifier resolved to. Named rather than four bare
// return values because the response echoes all of it: which remote was read is part
// of the answer, not an implementation detail. Policy resolution for a path goes
// through that remote's URL, so a caller that disagrees with the verdict needs to see
// whether the connector looked at the remote they meant.
type repoIdentity struct {
	Path      string // local path, empty when the input was a URL or slug
	Slug      string // host/owner/repo, empty when no remote could be read
	Kind      string // "path" | "url" | "slug" — how the input was read
	Remote    string // the remote whose URL produced Slug; only set for Kind=="path"
	RemoteURL string // that remote's URL, credentials stripped before reporting
	Warning   string // why the slug is missing, when it is and that costs something
}

// listRemotes runs `git remote` and returns the names, for the error above. Naming
// the real remotes turns "that is wrong" into "here is what to use", and the caller
// has no other way to find them when remote_list is disabled on the instance.
func listRemotes(c *connector.Ctx, repoPath string) []string {
	res, err := Run(c.Context(),
		Cmd{RepoPath: repoPath, UserArgs: []string{"remote"}},
		runOpts(c, false))
	if err != nil || !res.OK {
		return nil
	}
	return remoteNames(res.Stdout)
}

// looksLikeRemoteURL covers the two shapes a clone URL comes in, plus the web URLs
// an operator actually has on their clipboard.
func looksLikeRemoteURL(s string) bool {
	low := strings.ToLower(s)
	// git:// is listed so a caller passing one gets the transport refusal from
	// ConvertRemote rather than having it read as a local path here.
	for _, p := range []string{"http://", "https://", "ssh://", "git://"} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	// scp-style: git@host:owner/repo.git. The colon must come before any slash, or
	// "d:/code/work" would parse as host "d".
	if at := strings.Index(s, "@"); at > 0 {
		if colon := strings.Index(s[at:], ":"); colon > 0 {
			return true
		}
	}
	return false
}

// repoURLOf reduces a URL to the repository it belongs to.
//
// The reason this exists: what an operator has on their clipboard is usually a page,
// not a clone URL — "https://bitbucket.org/org/repo/pull-requests/18/diff". Left
// alone, RepoSlug returns "bitbucket.org/org/repo/pull-requests/18/diff", which
// matches no rule and reports the fallback policy as if the repository had no
// override. Cutting at the first path segment that cannot be part of a repository
// name turns the link into the answer.
func repoURLOf(raw string) string {
	// Everything after these belongs to the hosting UI, not to the repository. Same
	// list across GitHub, Bitbucket and GitLab.
	const cutMarkers = "pull-requests,pull,pulls,merge_requests,-,commits,commit,branches,branch,src,tree,blob,issues,wiki,compare,releases,tags,actions,pipelines,settings,downloads"

	scheme, rest := "", raw
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme, rest = raw[:i+3], raw[i+3:]
	}
	// Query and fragment are UI state.
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	segs := strings.Split(strings.TrimRight(rest, "/"), "/")
	// segs[0] is host (or user@host); a repository is host/owner/name, so there is
	// nothing to cut before index 3.
	for i := 1; i < len(segs); i++ {
		for _, m := range strings.Split(cutMarkers, ",") {
			if strings.EqualFold(segs[i], m) {
				return scheme + strings.Join(segs[:i], "/")
			}
		}
	}
	// No marker found. Keep host/owner/name and drop any deeper path, which is what a
	// link to a file inside a repository looks like.
	if len(segs) > 3 {
		return scheme + strings.Join(segs[:3], "/")
	}
	return scheme + strings.Join(segs, "/")
}

// isBareSlug recognises host/owner/repo typed without a scheme. Deliberately strict:
// it must have at least three segments and a dot in the first, so a relative path
// like "work/api" is treated as a path and validated rather than silently accepted
// as a slug that matches nothing.
func isBareSlug(s string) bool {
	if strings.ContainsAny(s, `\:`) {
		return false // a Windows path, or a scp-style remote
	}
	segs := strings.Split(strings.Trim(s, "/"), "/")
	return len(segs) >= 3 && strings.Contains(segs[0], ".")
}

// PolicyShowInput asks what the rules are for one repository.
//
// One field, accepting either identifier, because the question is asked at two
// different times and only one of them has a path:
//
//   - Before a clone there IS no local path, and that is exactly when the rules
//     matter — a branch name has to satisfy the policy before the work starts.
//   - After a clone the path is what every other operation takes.
//
// Requiring a path made the first case unanswerable, and a separate {remote} input
// was the wrong shape for it too: a remote NAME only means something inside a
// checkout, so it could not identify a repository that had not been cloned yet.
// A remote name is still accepted, under this same field, when it is a checkout.
type PolicyShowInput struct {
	Repo   string `wick:"required;desc=The repository to resolve rules for. Accepts a local path (d:/code/work/api or /srv/code/api), a clone URL (https://bitbucket.org/org/repo.git or git@github.com:org/repo.git), or host/owner/repo (bitbucket.org/org/repo). A URL works BEFORE the repository is cloned, which is when a branch name has to be chosen. Any URL is accepted — a web URL such as a pull-request link is reduced to its repository."`
	Remote string `wick:"desc=Only used when {repo} is a local path: which remote identifies it for host/owner/repo rules. Defaults to origin. Ignored when {repo} is already a URL."`
}

// doPolicyShow resolves the effective policy for one repository and describes it.
//
// The resolution path is policyFor — the same function execute calls — so what this
// reports is what the operations enforce. Re-deriving it here would let the answer
// drift from the enforcement, which is worse than not answering at all.
func doPolicyShow(c *connector.Ctx) (any, error) {
	// repo_path is accepted as an alias so a caller that already learned the name
	// every other operation uses is not told its input is missing.
	raw := firstNonEmpty(strings.TrimSpace(c.Input("repo")), strings.TrimSpace(c.Input("repo_path")))
	if raw == "" {
		return nil, errors.New("repo is required: a local path, a clone URL, or host/owner/repo")
	}

	id, err := resolveRepoIdentity(c, raw, strings.TrimSpace(c.Input("remote")))
	if err != nil {
		return nil, err
	}
	pol := policyFor(c, id.Path, id.Slug)

	out := map[string]any{
		// Both identifiers are echoed because MatchRepo tries BOTH, so a rule that did
		// not fire is explained by which of the two came back empty.
		"repo_path":     id.Path,
		"repo_slug":     id.Slug,
		"resolved_from": id.Kind,
		"matched_rule":  pol.MatchedRule,
		"rules":         policyRules(pol),
		"gates":         opGates(),
	}
	// Which remote was read, when one was. A path-based lookup resolves host/owner/repo
	// through a remote URL, and "origin" is only a default — a caller who disagrees with
	// the verdict has to be able to see that the connector read the remote they meant.
	if id.Remote != "" {
		out["remote"] = map[string]any{
			"name": id.Remote,
			"url":  StripCredentials(id.RemoteURL),
		}
	}
	if id.Slug == "" {
		note := "This repository has no remote, so host/owner/repo is unknown and only rules " +
			"written against the local path can match."
		// When stricter rules exist that could not be evaluated, say so: a policy that
		// reads as permissive because half of it was unevaluable is the one thing a caller
		// must not take at face value.
		if id.Warning != "" {
			note += " " + id.Warning + "."
		}
		out["note"] = note
	}
	// A pattern that does not compile blocks every mutation while leaving reads
	// working. Reporting it first, at the top level, means an agent sees the reason
	// its writes are failing instead of concluding the rules are unsatisfiable.
	if pol.PolicyErr != "" {
		out["blocked"] = true
		out["blocked_reason"] = pol.PolicyErr +
			" — every mutating operation is refused until the connector config is fixed. Reads still work."
	}
	return out, nil
}

// policyRules describes each rule in the shape an agent needs to comply without
// guessing: the value, the language it is written in, whether it is set, and what
// the unset state permits.
//
// syntax is the field that stops the guessing. Two languages appear in one policy —
// RE2 regexes and globs — and they are similar enough to confuse. Naming the
// language per rule, rather than once in prose, keeps it attached to the value it
// applies to.
func policyRules(p EffectivePolicy) map[string]any {
	branch := map[string]any{
		"pattern": p.BranchPattern,
		"syntax":  syntaxRE2,
		"applies_to": "The name of a branch being CREATED (branch_create, or checkout with create). " +
			"Not checked when pushing to a branch that already exists.",
	}
	if p.BranchPattern == "" {
		branch["enforced"] = false
		branch["effect"] = "No pattern is set, so any branch name is accepted."
	} else {
		branch["enforced"] = true
		branch["effect"] = "A branch name that does not match is refused before git runs."
		// A worked example beats the regex alone: an agent that must invent a name is
		// being asked to solve the regex, and the pattern's own author already knows an
		// answer. Only offered when one can be derived without guessing.
		if ex := branchExample(p.BranchPattern); ex != "" {
			branch["example_accepted"] = ex
		}
	}

	message := map[string]any{
		"pattern":    p.MessagePattern,
		"syntax":     syntaxRE2,
		"applies_to": "The whole commit message, on commit only. A push carries no message of its own, and merge/rebase messages are git's rather than the caller's.",
	}
	if p.MessagePattern == "" {
		message["enforced"] = false
		message["effect"] = "No pattern is set, so any commit message is accepted."
	} else {
		message["enforced"] = true
		message["effect"] = "A commit whose message does not match is refused before git runs. An empty message is left to git, which refuses it with its own error."
		// A message pattern needs the example MORE than a branch pattern does. The
		// conventional-commit shape carries a length floor (".{10,}") and an optional
		// scope group, and neither is obvious from reading the regex — the natural
		// guess, "fix: wip", satisfies the prefix and fails the length.
		if ex := messageExample(p.MessagePattern); ex != "" {
			message["example_accepted"] = ex
		}
	}

	protected := map[string]any{
		"branches": p.Protected,
		"syntax":   syntaxGlob,
		// Derived from mutatingOps, not written by hand. The hand-written version said
		// "push, commit, merge and pull" and was simply wrong: Evaluate checks
		// IsProtected for EVERY mutating operation, so checkout and branch_create are
		// refused too — and an agent reading the shorter list concluded a checkout to main
		// was safe. A list that has to be kept in sync with a map by hand will drift,
		// so it is read off the map instead.
		"applies_to": "Every operation that changes the repository, when it targets a matching branch: " +
			strings.Join(mutatingOpNames(), ", ") +
			". This includes checkout — a protected branch cannot be switched to, not only written to. " +
			"Reads (status, log, diff, show, branch_list) are never affected.",
	}
	if len(p.Protected) == 0 {
		protected["enforced"] = false
		protected["effect"] = "No branch is protected, so a direct push to main is allowed."
	} else {
		protected["enforced"] = true
		protected["effect"] = "Work on a matching branch must go through a branch that is not protected."
	}

	force := map[string]any{
		"allowed":    p.AllowForcePush,
		"applies_to": "push --force / --force-with-lease, and reset --hard.",
	}
	if p.AllowForcePush {
		force["effect"] = "Force push and hard reset are permitted."
	} else {
		force["effect"] = "Force push and hard reset are refused. A normal push and a soft or mixed reset still work."
	}

	raw := map[string]any{
		"enabled":    p.RawEnabled,
		"applies_to": "The raw operation, which passes a subcommand straight to git.",
	}
	if p.RawEnabled {
		// Report the allow-list explicitly. "raw is enabled" alone would invite an
		// agent to try arbitrary subcommands, every one of which is denied unless
		// listed.
		allowed := make([]string, 0, len(p.RawRules))
		for sub, mode := range p.RawRules {
			if mode == "allow" {
				allowed = append(allowed, sub)
			}
		}
		sortStrings(allowed)
		raw["allowed_subcommands"] = allowed
		raw["effect"] = "Only the listed subcommands run. Anything unlisted is denied."
	} else {
		raw["effect"] = "raw is disabled, so every subcommand is denied."
	}

	return map[string]any{
		"branch_pattern":         branch,
		"commit_message_pattern": message,
		"protected_branches":     protected,
		"force_push":             force,
		"raw":                    raw,
	}
}

// The two languages a policy is written in, stated per rule. Anchoring is called
// out because an unanchored pattern is the failure that looks like success: "fix/"
// alone also accepts "hotfix/nope".
const (
	syntaxRE2 = "RE2 regular expression (Go's regexp package: no lookahead, no backreferences). " +
		"Unanchored unless written with ^ and $, so a bare \"fix/\" also matches \"hotfix/x\"."
	syntaxGlob = "Comma-separated glob patterns where * is the only wildcard. Not regular expressions."
)

// mutatingOpNames lists every operation the protected-branch rule can refuse, read
// off mutatingOps so the description cannot drift from the code that enforces it.
// Sorted, because map order is randomised and two identical calls must not look like
// two different answers.
func mutatingOpNames() []string {
	out := make([]string, 0, len(mutatingOps))
	for op := range mutatingOps {
		out = append(out, op)
	}
	sortStrings(out)
	return out
}

// opGates maps each operation to the rules that judge it.
//
// DERIVED from mutatingOps and from Evaluate's own order, not hand-written. The
// hand-written version disagreed with the hand-written applies_to line inside the
// same response — one said checkout was gated by the protected list and the other did
// not — and an agent that believed the wrong one tried a checkout it was refused.
// Two prose lists describing one map is one list too many.
//
// This is the part an agent cannot infer from the rules alone, and getting it wrong
// costs calls in both directions: assuming the branch pattern gates a push makes a
// legal push look impossible, and assuming it does not gate branch_create makes a
// refusal look like a bug.
func opGates() map[string]any {
	// Rules that apply to a mutation regardless of which one it is. Evaluate checks
	// these inside a single `if mutatingOps[r.Op]` gate.
	universal := []string{"protected_branches"}

	// Rules that need something only some operations carry. The condition is stated
	// rather than implied: "force_push (when forced)" tells a caller that an unforced
	// push is not subject to it, which a bare rule name does not.
	extra := map[string][]string{
		"branch_create": {"branch_pattern"},
		"checkout":      {"branch_pattern (when creating a branch)"},
		"commit":        {"commit_message_pattern"},
		"push":          {"force_push (when force is set)"},
		"reset":         {"force_push (when mode is hard)"},
		"raw":           {"raw allow-list"},
	}

	gates := make(map[string]any, len(mutatingOps)+1)
	for _, op := range mutatingOpNames() {
		rules := append([]string(nil), universal...)
		rules = append(rules, extra[op]...)
		gates[op] = rules
	}
	// raw is judged by its allow-list alone — Evaluate returns from evaluateRaw before
	// the branch checks run, so listing protected_branches against it would be wrong.
	gates["raw"] = extra["raw"]

	gates["_reads"] = "status, log, diff, show, branch_list, remote_list, ls_remote, policy_show " +
		"and stash with action=list are never blocked by policy, including when the config " +
		"is malformed. stash with action=push or pop IS gated — the action decides, not the " +
		"operation name."
	return gates
}

// branchExample derives a branch name the pattern accepts, for the alternation-of-
// prefixes shape nearly every real branch policy uses:
//
//	^(fix|feat|chore)/[a-z0-9._-]+$
//
// It VERIFIES the candidate against the pattern before returning it and returns ""
// when it cannot produce a match. A wrong example is worse than none: an agent
// would copy it and be refused, having been told the refusal was impossible.
func branchExample(pattern string) string {
	re, err := regexpCompile(pattern)
	if err != nil {
		return ""
	}
	// Prefixes read off the pattern first, then generic fallbacks. Every candidate is
	// tested, so a wrong guess costs nothing and an exotic pattern simply yields no
	// example.
	for _, prefix := range branchPrefixes(pattern) {
		for _, suffix := range []string{"example-change", "example", "1234"} {
			if cand := prefix + suffix; re.MatchString(cand) {
				return cand
			}
		}
	}
	for _, cand := range []string{
		"fix/example-change", "feat/example-change", "chore/example-change",
		"example-change", "main",
	} {
		if re.MatchString(cand) {
			return cand
		}
	}
	return ""
}

// messageExample derives a commit message the pattern accepts, using the same
// read-the-prefix-then-verify approach as branchExample.
//
// The subject text is what varies here, not the prefix: a conventional-commit
// pattern usually ends in a length floor like ".{10,}" or ".{1,72}$", so the
// candidates run from a realistic subject up to a long one. Every candidate is
// tested, and "" is returned rather than a guess.
func messageExample(pattern string) string {
	re, err := regexpCompile(pattern)
	if err != nil {
		return ""
	}
	// Subjects, shortest realistic first, then longer ones for a higher floor. Long
	// enough to clear the floors real policies set, short enough to stay under a
	// ".{1,72}" ceiling.
	subjects := []string{
		"stop the login timeout",
		"correct the timeout handling in the session refresh path",
	}
	for _, prefix := range branchPrefixes(pattern) {
		for _, subj := range subjects {
			// Separators are ordered so the prefix is used AS WRITTEN first. A pattern
			// whose literal run already ends in ": " (^fix: .+) otherwise gets ": " appended
			// again and yields "fix: : subject" — it matches, but an agent copying it
			// commits nonsense, and a technically-correct example that reads as a mistake
			// is nearly as bad as a wrong one.
			for _, sep := range []string{"", ": ", " "} {
				cand := prefix + sep + subj
				// Skip a candidate whose seam is visibly wrong even if the regex accepts it.
				if strings.Contains(cand, ":  ") || strings.HasPrefix(cand, ":") ||
					strings.HasPrefix(cand, " ") {
					continue
				}
				if re.MatchString(cand) {
					return cand
				}
			}
		}
	}
	for _, cand := range []string{
		"fix: stop the login timeout",
		"feat: add the missing retry",
		"ABC-123 stop the login timeout",
		"stop the login timeout",
	} {
		if re.MatchString(cand) {
			return cand
		}
	}
	return ""
}

// branchPrefixes reads the literal prefixes a pattern will accept, walking the
// pattern left to right and expanding each alternation group it meets.
//
// It handles the three shapes real branch policies use, which the earlier
// single-group version did not:
//
//	^(fix|feat)/[a-z-]+$        → "fix/", "feat/"
//	^ai/(fix|feat)/[a-z-]+$     → "ai/fix/", "ai/feat/"   (literal before the group)
//	^feature/.+$                → "feature/"              (no group at all)
//
// Everything that is not a literal or a literal-only alternation ends the walk: the
// remainder is left to the candidate suffixes, which are verified against the real
// regex anyway.
func branchPrefixes(pattern string) []string {
	s := strings.TrimPrefix(pattern, "^")
	prefixes := []string{""}

	for s != "" {
		switch {
		case s[0] == '(':
			end := strings.Index(s, ")")
			if end < 0 {
				return prefixes
			}
			alts := literalAlternatives(strings.TrimPrefix(s[1:end], "?:"))
			if len(alts) == 0 {
				return prefixes
			}
			// Cross-product: each prefix so far gains each alternative.
			next := make([]string, 0, len(prefixes)*len(alts))
			for _, p := range prefixes {
				for _, a := range alts {
					next = append(next, p+a)
				}
			}
			prefixes = next
			s = s[end+1:]
			// A quantifier on the group makes the whole thing optional or repeatable, so
			// the prefix is no longer certain — stop here rather than guess.
			if s != "" && strings.ContainsRune("?*+{", rune(s[0])) {
				return prefixes
			}
		case isLiteralByte(s[0]):
			for i := range prefixes {
				prefixes[i] += s[:1]
			}
			s = s[1:]
		default:
			// A metacharacter: the literal run is over.
			return prefixes
		}
	}
	return prefixes
}

// literalAlternatives splits an alternation body, returning nothing unless EVERY
// branch is a plain literal. A group where one branch carries metacharacters cannot
// be expanded into prefixes without interpreting the regex.
func literalAlternatives(inner string) []string {
	if inner == "" {
		return nil
	}
	alts := strings.Split(inner, "|")
	for _, a := range alts {
		if a == "" {
			return nil
		}
		for i := 0; i < len(a); i++ {
			if !isLiteralByte(a[i]) {
				return nil
			}
		}
	}
	return alts
}

// isLiteralByte reports whether a byte stands for itself in a regex. Deliberately
// conservative: anything that could be syntax is treated as syntax, because the cost
// of being wrong is an example that does not match.
func isLiteralByte(b byte) bool {
	return !strings.ContainsRune(`\[](){}.*+?^$|`, rune(b))
}

// sortStrings keeps the raw allow-list stable across calls. Map iteration order is
// randomised, and an agent comparing two responses should not see a difference that
// is not there.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// policySummary renders the same facts as one line, for the deny path. See
// deniedEnvelope: a refusal names the rule that fired, and this names where the
// full answer lives.
func policySummary(op string) string {
	return fmt.Sprintf("Call policy_show with the same repo_path to see every rule that applies "+
		"before retrying %s, instead of discovering them one refusal at a time.", op)
}
