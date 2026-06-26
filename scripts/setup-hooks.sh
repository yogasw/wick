#!/bin/sh
# One-time per-clone setup: point Git at the versioned hooks in .githooks/.
#
#   sh scripts/setup-hooks.sh
#
# This wires the mandatory local test gate (.githooks/pre-push). Bridge hooks
# (post-commit / post-checkout) still delegate to graphify when it's installed.
set -e

ROOT=$(git rev-parse --show-toplevel)
cd "$ROOT"

git config core.hooksPath .githooks
chmod +x .githooks/* 2>/dev/null || true

echo "Hooks enabled: core.hooksPath -> .githooks"
echo "  pre-push       run go build + go test before every push"
echo "  post-commit    delegates to graphify (if installed)"
echo "  post-checkout  delegates to graphify (if installed)"
