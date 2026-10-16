#!/usr/bin/env bash
# Read-only check + summary for "Merge develop to master"
# (.github/workflows/merge-develop-to-master.yml). Runs the same way on a
# laptop for a dry-run:
#
#   .github/scripts/develop-summary.sh origin/develop origin/master /tmp/body.md
#
# Writes key=value lines to stdout (append them to $GITHUB_OUTPUT) and the
# commit list to <out-file>, which becomes the PR body and the squash commit
# body. Never writes to the repo.
#
#   develop_sha   full sha of <develop-ref>
#   behind        commits on master that develop does not have (must be 0)
#   has_changes   false when the develop and master trees are identical
#   last_tag      newest v<semver> tag by creation date ("" when none),
#                 shown in the summary only
#   last_promoted develop sha recorded by the previous squash ("" when none)
#   count         commits listed in <out-file>
#
# Range: develop commits that are not on master and not in the previous
# squash. Master only ever gets squash commits, so "not on master" alone would
# list already promoted develop commits again; every squash body carries a
# line "Develop-Head: <40-hex sha>" that marks where the previous promotion
# stopped (any line of the body, the last such line of a commit wins). The
# last v* tag is not a bound of its own: release tags sit on a bundle commit
# whose parents are on master, so ^master already excludes everything in it.
# Merge commits are left out (PR merges and master → develop syncs); the
# commits they bring in are listed.
set -euo pipefail

if [ $# -ne 3 ]; then
  echo "usage: $0 <develop-ref> <master-ref> <out-file>" >&2
  exit 2
fi
develop=$1
master=$2
out=$3

develop_sha=$(git rev-parse --verify "$develop^{commit}")
master_sha=$(git rev-parse --verify "$master^{commit}")
behind=$(git rev-list --count "$develop_sha..$master_sha")

has_changes=true
if git diff --quiet "$master_sha" "$develop_sha" --; then
  has_changes=false
fi

# Creation date, not version order: a stray higher tag must not hide the
# real latest release. Plugin tags (<name>/v<x>) never match.
last_tag=$(git for-each-ref --sort=-creatordate --format='%(refname:short)' 'refs/tags/v*' \
  | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true)

# Newest master commit with a usable Develop-Head line. A line that starts
# with "Develop-Head:" but is not a full sha, or names a commit this clone
# does not have, is reported and skipped.
last_promoted=""
while IFS=$'\t' read -r commit value; do
  if printf '%s' "$value" | grep -Eq '^[0-9a-f]{40}$' && git cat-file -e "$value^{commit}" 2>/dev/null; then
    last_promoted=$value
    break
  fi
  echo "::warning::ignoring malformed or unknown Develop-Head in ${commit:0:8}: '$value'" >&2
done < <(git log "$master_sha" -n 500 --format='@@commit@@%H%n%B' | awk '
  /^@@commit@@/ { if (seen) print c "\t" v; c = substr($0, 11); v = ""; seen = 0; next }
  /^Develop-Head:/ { sub(/^Develop-Head:[ \t]*/, ""); sub(/[ \t\r]+$/, ""); v = $0; seen = 1 }
  END { if (seen) print c "\t" v }
')

exclude=("^$master_sha")
[ -n "$last_promoted" ] && exclude+=("^$last_promoted")

max=300
count=$(git rev-list --no-merges --count "$develop_sha" "${exclude[@]}")
{
  if [ "$count" -eq 0 ]; then
    echo "- (no new commits; merge commits only)"
  else
    git log --no-merges --format='- %s (%h)' -n "$max" "$develop_sha" "${exclude[@]}"
    if [ "$count" -gt "$max" ]; then
      echo "- … and $((count - max)) more"
    fi
  fi
  echo
  echo "Develop-Head: $develop_sha"
} > "$out"

echo "develop_sha=$develop_sha"
echo "behind=$behind"
echo "has_changes=$has_changes"
echo "last_tag=$last_tag"
echo "last_promoted=$last_promoted"
echo "count=$count"
