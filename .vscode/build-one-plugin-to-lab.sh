#!/usr/bin/env bash
# Build ONE connector plugin from current source and install its unzipped
# { binary, plugin.json } into wick-lab's plugin dir
# (~/.wick-lab/plugins/connectors/<name>/), where the running lab scans.
#
# Same layout as build-plugins-to-lab.sh, but for a single plugin so an
# iteration on one connector doesn't rebuild every plugin. The plugin name is
# passed as $1 (the "plugin: build ONE → lab" task fills it from a picker).
#
# Lives as a standalone file (not inline in tasks.json) so the shell that runs
# the VS Code task can't mangle nested quoting. Run from the repo root.
set -euo pipefail

name="${1:-}"
if [ -z "$name" ]; then
  echo "error: no plugin name given (usage: build-one-plugin-to-lab.sh <name>)"
  exit 1
fi
if [ ! -d "plugins/connector/$name" ]; then
  echo "error: plugins/connector/$name does not exist"
  echo "available:"; ls plugins/connector/ | grep -v '^_'
  exit 1
fi

command -v unzip >/dev/null 2>&1 || { echo "error: 'unzip' not found — install it (e.g. apt install unzip)"; exit 1; }

# Resolve the wick-lab home the SAME way the Windows wick-lab binary does:
# %USERPROFILE%\.wick-lab (= ~/.wick-lab on that OS user). Do NOT use $HOME —
# the VS Code task shell may be msys2/zsh whose $HOME is /home/<user>, a
# DIFFERENT folder than C:\Users\<user> that the Windows binary scans, so the
# plugin would install where the running lab never looks. USERPROFILE is set
# consistently across git-bash, msys2, and cmd on Windows; fall back to $HOME on
# non-Windows.
labhome="$HOME"
if [ -n "${USERPROFILE:-}" ]; then
  if command -v cygpath >/dev/null 2>&1; then
    labhome="$(cygpath -u "$USERPROFILE")"
  else
    # C:\Users\x -> /c/Users/x (drive letter lower-cased, backslashes → slashes)
    drive="$(printf '%s' "$USERPROFILE" | cut -c1 | tr 'A-Z' 'a-z')"
    rest="$(printf '%s' "$USERPROFILE" | cut -c3- | tr '\\' '/')"
    labhome="/$drive$rest"
  fi
fi
root="$labhome/.wick-lab/plugins/connectors"
tmp="$(mktemp -d)"

cli="$tmp/wick"
case "$(uname -s)" in
  *NT* | *MINGW* | *MSYS* | CYGWIN*) cli="$tmp/wick.exe" ;;
esac

go build -o "$cli" .
(cd plugins && "$cli" plugin build --kind connector --output "$tmp" "$name")

shopt -s nullglob
zips=("$tmp"/*.zip)
if [ ${#zips[@]} -eq 0 ]; then
  echo "no plugin built for '$name'"
  exit 1
fi

mkdir -p "$root"
for zip in "${zips[@]}"; do
  base=$(basename "$zip")
  pname="${base%%-*}"
  dest="$root/$pname"
  # A running lab holds the OS file lock on the plugin binary, so overwriting it
  # fails with "Device or resource busy" (Windows) / "text file busy" (Linux).
  # Kill the running subprocess first; the lab's hot-reload poller re-spawns it
  # from the freshly-installed binary within ~5s. Best-effort — no process is
  # fine (nothing to kill).
  case "$(uname -s)" in
    *NT* | *MINGW* | *MSYS* | CYGWIN*)
      taskkill //F //IM "$pname.exe" >/dev/null 2>&1 || true ;;
    *)
      pkill -f "/connectors/$pname/$pname" >/dev/null 2>&1 || true ;;
  esac
  rm -rf "$dest"
  mkdir -p "$dest"
  unzip -oq "$zip" -d "$dest"
  echo "installed $pname -> $dest"
done
