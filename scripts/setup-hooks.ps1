# One-time per-clone setup (Windows): point Git at the versioned hooks in .githooks/.
#
#   pwsh scripts/setup-hooks.ps1   (or)   powershell -File scripts/setup-hooks.ps1
#
# Wires the mandatory local test gate (.githooks/pre-push). Git for Windows
# runs the sh hooks via its bundled Bash, so no extra setup is needed.
$ErrorActionPreference = 'Stop'

$root = git rev-parse --show-toplevel
Set-Location $root

git config core.hooksPath .githooks

Write-Host "Hooks enabled: core.hooksPath -> .githooks"
Write-Host "  pre-push       run go build + go test before every push"
Write-Host "  post-commit    delegates to graphify (if installed)"
Write-Host "  post-checkout  delegates to graphify (if installed)"
