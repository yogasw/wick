#!/usr/bin/env bash
# Uninstall every connector plugin from wick-lab's plugin dir. The running lab's
# hot-reload poller drops them within ~5s — no server restart needed.
#
# Standalone file (not inline in tasks.json) so the task shell — zsh on this
# machine — can't mangle the quoting. Invoked by the "plugin: clear lab" task.
set -euo pipefail

root="$HOME/.wick-lab/plugins/connectors"
rm -rf "$root"
mkdir -p "$root"
echo "cleared $root"
