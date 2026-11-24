#!/usr/bin/env bash
# Format and validate one connector in a dedicated development checkout.
# This never installs a plugin, changes the core binary, commits, or pushes.
set -euo pipefail

dir="${1:?usage: prepare-connector.sh <checkout> <connector_key>}"
key="${2:?connector key required}"
[[ "$key" =~ ^[a-z0-9_]+$ ]] || { echo "invalid connector key" >&2; exit 2; }

cd "$dir"
[ -d "plugins/connector/$key" ] || { echo "connector not found" >&2; exit 2; }
echo "== format $key"
gofmt -w "plugins/connector/$key"
echo "== quality gate"
bash scripts/check-connector-quality.sh "$key"
echo "== formatting diff"
git diff --stat -- "plugins/connector/$key"
