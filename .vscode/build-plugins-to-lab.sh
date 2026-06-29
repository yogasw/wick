#!/usr/bin/env bash
# Build EVERY connector plugin from current source and install each unzipped
# { binary, plugin.json } into wick-lab's plugin dir
# (~/.wick-lab/plugins/connectors/<name>/), where the running lab scans.
#
# Lives as a standalone file (not inline in tasks.json) so the shell that runs
# the VS Code task — zsh on this machine — can't mangle the nested quoting.
# Invoked by the "plugin: build all → lab" task; run from the repo root.
set -euo pipefail

# unzip is bundled with Git Bash/msys2 on Windows but may be absent on a lean
# Linux box — fail with a clear hint instead of a bare "command not found".
command -v unzip >/dev/null 2>&1 || { echo "error: 'unzip' not found — install it (e.g. apt install unzip)"; exit 1; }

root="$HOME/.wick-lab/plugins/connectors"
tmp="$(mktemp -d)"

# Windows needs the .exe suffix or the built CLI can't be exec'd; Linux/macOS
# leave it off. Detect via the OS reported by the shell (Git Bash/msys2 → *NT*).
cli="$tmp/wick"
case "$(uname -s)" in
  *NT* | *MINGW* | *MSYS* | CYGWIN*) cli="$tmp/wick.exe" ;;
esac

go build -o "$cli" .
(cd plugins && "$cli" plugin build --kind connector --all-plugins --output "$tmp")

mkdir -p "$root"
shopt -s nullglob
zips=("$tmp"/*.zip)
if [ ${#zips[@]} -eq 0 ]; then
  echo "no plugins built"
  exit 0
fi

for zip in "${zips[@]}"; do
  base=$(basename "$zip")
  name="${base%%-*}"
  dest="$root/$name"
  rm -rf "$dest"
  mkdir -p "$dest"
  unzip -oq "$zip" -d "$dest"
  echo "installed $name -> $dest"
done
