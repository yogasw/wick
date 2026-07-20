#!/usr/bin/env bash
# preLaunchTask for the "plugin debug (loki)" launch config. Installs the
# debugged plugin's {binary, plugin.json} into wick-lab's plugin dir so the
# connector REGISTERS and shows in the list, BEFORE dlv runs the plugin for
# breakpoints. The manifest is what a plain `dlv debug` never writes, which is
# why the connector goes missing when you only run the debug config.
#
# Target comes from WICK_DEBUG_PLUGIN in .env (the same key the launch config +
# wick-lab reattach use), so all three agree. Lives as a file (not inline in
# tasks.json) so the task shell can't mangle the quoting.
set -euo pipefail
cd "$(dirname "$0")/.."

name=""
if [ -f .env ]; then
  name="$(grep -E '^WICK_DEBUG_PLUGIN=' .env | tail -1 | cut -d= -f2- | tr -d '"'"'"' \r')"
fi
if [ -z "$name" ]; then
  echo "WICK_DEBUG_PLUGIN not set in .env — set it to the plugin you're debugging (e.g. loki)."
  exit 1
fi

exec bash .vscode/build-one-plugin-to-lab.sh "$name"
