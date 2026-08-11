#!/usr/bin/env bash
# preLaunchTask for the "plugin debug (loki)" launch config. Installs the
# debugged plugin's {binary, plugin.json} into wick-lab's plugin dir so the
# connector REGISTERS and shows in the list, BEFORE dlv runs the plugin for
# breakpoints. The manifest is what a plain `dlv debug` never writes, which is
# why the connector goes missing when you only run the debug config.
#
# Target comes from WICK_DEBUG_PLUGIN — process env first, then .env (see below),
# the same key the launch config and wick-lab reattach use, so all three agree.
# Lives as a file (not inline in tasks.json) so the task shell can't mangle the
# quoting.
set -euo pipefail
cd "$(dirname "$0")/.."

# Process env wins, .env is the fallback.
#
# Both orders matter because TWO different consumers read this key:
#   - this script, to pick which plugin gets built + its manifest installed
#   - internal/connectors/plugin/pool.go, via os.Getenv, to pick which plugin to
#     reattach to under dlv
#
# A launch config's "env" block reaches pool.go but NOT a preLaunchTask — VS Code
# does not pass launch env to tasks. Reading the process env first means setting
# the key in launch.json alone is enough; without it the script silently
# installed whatever .env still named, wiping the intended plugin's manifest and
# making the connector vanish from the UI.
name="${WICK_DEBUG_PLUGIN:-}"
if [ -z "$name" ] && [ -f .env ]; then
  name="$(grep -E '^WICK_DEBUG_PLUGIN=' .env | tail -1 | cut -d= -f2- | tr -d '"'"'"' \r')"
fi
if [ -z "$name" ]; then
  echo "WICK_DEBUG_PLUGIN not set — set it in .env or in the launch config's env block (e.g. git)."
  exit 1
fi

exec bash .vscode/build-one-plugin-to-lab.sh "$name"
