#!/usr/bin/env bash
# Format and validate one connector in a dedicated development checkout.
# With an optional ref and destination, prepare a new detached Git worktree
# first. The existing checkout and installed Wick binary stay untouched.
# This script never installs a plugin, commits, or pushes.
set -euo pipefail

dir="${1:?usage: prepare-connector.sh <checkout> <connector_key> [ref worktree]}"
key="${2:?connector key required}"
[[ "$key" =~ ^[a-z0-9_]+$ ]] || { echo "invalid connector key" >&2; exit 2; }

if [ "$#" -eq 4 ]; then
  ref="$3"
  dest="$4"
  git -C "$dir" check-ref-format --branch "$ref" >/dev/null
  [ ! -e "$dest" ] || { echo "worktree destination exists" >&2; exit 2; }
  echo "== fetch development ref"
  git -C "$dir" fetch origin "$ref"
  git -C "$dir" worktree add --detach "$dest" FETCH_HEAD
  dir="$dest"
elif [ "$#" -ne 2 ]; then
  echo "expected checkout/key or checkout/key/ref/worktree" >&2
  exit 2
fi

cd "$dir"
[ -d "plugins/connector/$key" ] || { echo "connector not found" >&2; exit 2; }
echo "== format $key"
gofmt -w "plugins/connector/$key"
echo "== quality gate"
bash scripts/check-connector-quality.sh "$key"
echo "== formatting diff"
git diff --stat -- "plugins/connector/$key"
