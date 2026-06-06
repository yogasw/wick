#!/usr/bin/env bash
# PreToolUse(Bash) hook — gates `gh pr create` so the doc-sync subagent
# reviews the current branch HEAD and syncs root docs/ before a PR goes
# out. Loop-safe: doc-sync writes the HEAD it reviewed to the marker; once
# the marker matches the current HEAD, the PR is allowed through (this also
# covers the "no docs needed" case, since doc-sync writes the marker even
# when it changes nothing).
#
# Fail-open: any unexpected condition (no jq, not a git repo, parse error)
# exits 0 = allow, so the hook can never wedge PR creation by accident.
set -uo pipefail

input=$(cat 2>/dev/null) || exit 0

# Need jq to read the tool command; without it, don't gate.
command -v jq >/dev/null 2>&1 || exit 0

cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null) || exit 0

# Only gate an actual `gh pr create` invocation — match when the command
# (after leading whitespace) STARTS with it. A substring match would also
# trip on commit messages / echoes that merely mention the phrase, so we
# anchor to the start instead.
trimmed="${cmd#"${cmd%%[![:space:]]*}"}"
case "$trimmed" in
  "gh pr create"*) ;;
  *) exit 0 ;;
esac

git_dir=$(git rev-parse --git-dir 2>/dev/null) || exit 0
head=$(git rev-parse HEAD 2>/dev/null) || exit 0
marker_file="$git_dir/wick-doc-sync-head"
marker=""
[ -f "$marker_file" ] && marker=$(cat "$marker_file" 2>/dev/null)

# doc-sync already reviewed this exact HEAD → allow the PR.
[ "$marker" = "$head" ] && exit 0

# Otherwise block and tell the main agent to spawn doc-sync first.
reason="Root docs/ not yet synced for HEAD ${head}. Before creating this PR you MUST spawn the doc-sync subagent (Agent tool, subagent_type: \"doc-sync\") to review the branch diff vs master and update root docs/ if the change warrants it. doc-sync writes the sync marker as its final step; then re-run this exact gh pr create command."

jq -n --arg r "$reason" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    permissionDecision: "deny",
    permissionDecisionReason: $r
  }
}'
exit 0
