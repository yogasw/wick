#!/usr/bin/env bash
# Check one connector package without changing the checkout. Use this before
# committing or releasing; pass a connector key such as host_access.
set -euo pipefail

key="${1:?usage: check-connector-quality.sh <connector_key>}"
[[ "$key" =~ ^[a-z0-9_]+$ ]] || { echo "invalid connector key" >&2; exit 2; }
cd "$(dirname "${BASH_SOURCE[0]}")/../plugins"
dir="connector/$key"
[ -d "$dir" ] || { echo "connector not found: $key" >&2; exit 2; }

echo "== Go formatting ($key)"
unformatted="$(gofmt -l "$dir")"
if [ -n "$unformatted" ]; then
  printf 'Run gofmt -w on:\n%s\n' "$unformatted" >&2
  exit 1
fi

# Enforce the public safeexec API for host-compatible connector code.
# AST-based vet checks still run below; this import guard deliberately
# forbids os/exec in connectors checked by this script.
echo "== safeexec import guard"
if grep -Rn --include='*.go' '"os/exec"' "$dir"; then
  echo "Use github.com/yogasw/wick/pkg/safeexec instead of os/exec" >&2
  exit 1
fi

echo "== Go vet ($key)"
go vet "./$dir/..."
echo "== Unit tests ($key)"
go test -timeout 5m "./$dir/..."
