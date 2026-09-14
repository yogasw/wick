package scm

import (
	"context"
	"strconv"
	"strings"
)

// history.go answers the question the Source panel's graph asks: for each
// commit, how far has it actually got? A commit sitting in the local repo,
// one already pushed to its branch, and one that has landed on the trunk are
// three very different states, and a flat list of subjects shows none of it.
//
// The states are computed from set membership, not from one git call per
// commit: two `rev-list` invocations bounded by the same --max-count as the
// log, and a commit's state is which of those sets it falls in. Filtering
// preserves walk order, so a commit inside the first N of the full walk is
// always inside the first N of a filtered one — which is what makes the
// bounded lists safe to use for membership.

// Commit states, in the order work travels through them.
const (
	// StateLocal — committed here and on no remote branch. This is the work
	// that disappears if the machine does.
	StateLocal = "local"
	// StatePushed — on a remote branch, but not yet on the trunk.
	StatePushed = "pushed"
	// StateTrunk — reachable from the trunk (origin/HEAD, or master/main).
	StateTrunk = "trunk"
)

// Reference selectors accepted by LogOptions.Refs, mirroring the ones the
// editor-style picker offers.
const (
	// RefsAuto — the checked-out branch and its upstream. The default.
	RefsAuto = "auto"
	// RefsAll — every local and remote-tracking branch.
	RefsAll = "all"
)

// LogOptions selects how much history to walk and which references.
type LogOptions struct {
	Limit int
	// Refs is the set of history item references to walk. Empty or
	// [RefsAuto] means the current branch plus its upstream; [RefsAll]
	// means every branch; anything else is taken as literal ref names.
	Refs []string
}

// refArgs turns the selector into git revision arguments.
func (o LogOptions) refArgs() []string {
	switch {
	case len(o.Refs) == 0:
		return []string{RefsAuto}
	case len(o.Refs) == 1 && strings.EqualFold(o.Refs[0], RefsAll):
		return []string{"--all"}
	case len(o.Refs) == 1 && strings.EqualFold(o.Refs[0], RefsAuto):
		return []string{RefsAuto}
	}
	out := make([]string, 0, len(o.Refs))
	for _, r := range o.Refs {
		r = strings.TrimSpace(r)
		// A ref name is interpolated into a git argument list, so refuse
		// anything that could read as an option or an escape.
		if r == "" || strings.HasPrefix(r, "-") || strings.ContainsAny(r, " \t\n") {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return []string{RefsAuto}
	}
	return out
}

// resolveAuto expands the RefsAuto placeholder to HEAD plus its upstream,
// so the graph shows what the branch has and what its remote has in one
// view — the divergence is the whole point of looking.
func resolveAuto(ctx context.Context, dir string, args []string) []string {
	if len(args) != 1 || args[0] != RefsAuto {
		return args
	}
	out := []string{"HEAD"}
	if up, err := run(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		if u := strings.TrimSpace(up); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// TrunkRef is the ref the graph treats as "landed": the remote's default
// branch when it publishes one, else the conventional names, else "".
// Detected rather than assumed — plenty of repos never had a branch called
// main, and plenty of others no longer have one called master.
func TrunkRef(ctx context.Context, dir string) string {
	if out, err := run(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if v := strings.TrimSpace(out); v != "" {
			return v
		}
	}
	for _, cand := range []string{"origin/master", "origin/main", "master", "main"} {
		if _, err := run(ctx, dir, "rev-parse", "--verify", "--quiet", cand+"^{commit}"); err == nil {
			return cand
		}
	}
	return ""
}

// revSet runs a bounded rev-list and returns the full shas it yields.
func revSet(ctx context.Context, dir string, limit int, args ...string) map[string]bool {
	full := append([]string{"rev-list", "--max-count=" + strconv.Itoa(limit)}, args...)
	out, err := run(ctx, dir, full...)
	if err != nil {
		// A missing ref (no remotes, no trunk) is an ordinary answer here,
		// not a failure: it just means the set is empty.
		return map[string]bool{}
	}
	set := make(map[string]bool)
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			set[ln] = true
		}
	}
	return set
}

// History walks the log and labels every commit with where it has got to.
func History(ctx context.Context, dir string, opts LogOptions) ([]LogEntry, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()

	refs := resolveAuto(ctx, dir, opts.refArgs())

	// %H full sha for set membership, %h short for display, %p parents for
	// the lanes, %D the refs pointing AT this commit for the badges.
	format := strings.Join([]string{"%H", "%h", "%s", "%an", "%cr", "%cI", "%p", "%D"}, logFieldSep) + logRecSep
	args := append([]string{"log", "--max-count=" + strconv.Itoa(limit), "--pretty=format:" + format}, refs...)
	out, err := run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}

	// Commits on no remote branch at all, and commits not on the trunk.
	// Both bounded by the same limit as the log above.
	unpushed := revSet(ctx, dir, limit, append(append([]string{}, refs...), "--not", "--remotes")...)
	offTrunk := map[string]bool{}
	trunk := TrunkRef(ctx, dir)
	if trunk != "" {
		offTrunk = revSet(ctx, dir, limit, append(append([]string{}, refs...), "--not", trunk)...)
	}

	entries := []LogEntry{}
	for _, rec := range strings.Split(out, logRecSep) {
		rec = strings.Trim(rec, "\n\r")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, logFieldSep)
		if len(f) < 8 {
			continue
		}
		e := LogEntry{
			SHA: f[1], Subject: f[2], Author: f[3], RelDate: f[4], ISODate: f[5],
			Parents: strings.Fields(f[6]),
			Refs:    parseDecoration(f[7]),
		}
		switch {
		case unpushed[f[0]]:
			e.State = StateLocal
		case trunk == "":
			// Nothing to land on, so "pushed" is as far as the scale goes.
			e.State = StatePushed
		case offTrunk[f[0]]:
			e.State = StatePushed
		default:
			e.State = StateTrunk
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// parseDecoration splits `%D` into plain ref names. "HEAD -> master" is two
// facts about the same commit, so it becomes two entries.
func parseDecoration(d string) []string {
	d = strings.TrimSpace(d)
	if d == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(d, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if head, branch, found := strings.Cut(part, " -> "); found {
			out = append(out, strings.TrimSpace(head), strings.TrimSpace(branch))
			continue
		}
		out = append(out, part)
	}
	return out
}

// HistoryRef is one selectable reference in the graph's picker.
type HistoryRef struct {
	Name string `json:"name"` // master, origin/master
	SHA  string `json:"sha"`  // short sha it points at
	// Remote marks a remote-tracking ref, which the picker groups apart.
	Remote bool `json:"remote"`
	// Current marks the checked-out branch — what RefsAuto follows.
	Current bool `json:"current"`
	// Trunk marks the ref commits are measured against for StateTrunk.
	Trunk bool `json:"trunk"`
}

// HistoryRefs lists every reference the graph can be pointed at.
func HistoryRefs(ctx context.Context, dir string) ([]HistoryRef, error) {
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	format := strings.Join([]string{"%(refname:short)", "%(objectname:short)", "%(HEAD)", "%(refname)"}, logFieldSep)
	out, err := run(ctx, dir, "for-each-ref", "--format="+format, "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	trunk := TrunkRef(ctx, dir)
	refs := []HistoryRef{}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		f := strings.Split(ln, logFieldSep)
		if len(f) < 4 || f[0] == "" {
			continue
		}
		// origin/HEAD is a symlink to another entry in this same list;
		// showing it would offer the same history twice under two names.
		if strings.HasSuffix(f[0], "/HEAD") {
			continue
		}
		refs = append(refs, HistoryRef{
			Name:    f[0],
			SHA:     f[1],
			Remote:  strings.HasPrefix(f[3], "refs/remotes/"),
			Current: strings.TrimSpace(f[2]) == "*",
			Trunk:   f[0] == trunk,
		})
	}
	return refs, nil
}
