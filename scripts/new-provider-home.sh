#!/usr/bin/env bash
# Create an isolated CLI home so one host can run several accounts of the same
# provider side by side — one wick provider instance per home.
#
# wick already supports this: a provider instance's Env is merged into every
# spawn AND into the login TTY, so an instance carrying CLAUDE_CONFIG_DIR /
# CODEX_HOME reads, writes and logs in to THAT home. What it has no opinion
# about is creating the home, which is what this script is for.
#
# What gets shared and what does not is the whole point, so it is spelled out
# per provider below rather than left to the person running this at 1am.
#
#   ./scripts/new-provider-home.sh            # interactive, enter through the defaults
#   ./scripts/new-provider-home.sh -y codex work
#
set -euo pipefail

YES=0
[ "${1:-}" = "-y" ] && { YES=1; shift; }
ARG_KIND="${1:-}"
ARG_NAME="${2:-}"

c_dim=$'\033[90m'; c_b=$'\033[1m'; c_ok=$'\033[32m'; c_warn=$'\033[33m'; c_0=$'\033[0m'
say()  { printf '%s\n' "$*"; }
note() { printf '%s%s%s\n' "$c_dim" "$*" "$c_0"; }
warn() { printf '%s%s%s\n' "$c_warn" "$*" "$c_0"; }

# ask <var> <prompt> <default>
ask() {
  local __v=$1 prompt=$2 def=$3 reply=
  if [ "$YES" = 1 ]; then printf -v "$__v" '%s' "$def"; return; fi
  read -r -p "$(printf '%s %s[%s]%s ' "$prompt" "$c_dim" "$def" "$c_0")" reply || true
  printf -v "$__v" '%s' "${reply:-$def}"
}
confirm() { # confirm <prompt>  → 0 on yes
  local reply=
  [ "$YES" = 1 ] && return 0
  read -r -p "$1 [y/N] " reply || true
  [[ "${reply,,}" == y* ]]
}

say ""
say "${c_b}Isolated provider home${c_0}"
note "Satu home = satu akun = satu provider instance di wick."
say ""

# ---------------------------------------------------------------- provider
ask KIND "Provider (claude / codex)?" "${ARG_KIND:-codex}"
KIND=${KIND,,}
case "$KIND" in
  claude)
    ENV_VAR=CLAUDE_CONFIG_DIR
    SRC_DEFAULT="$HOME/.claude"
    DIR_FMT="$HOME/.claude-%s"
    # Shared by default: skills and plugins are content, projects/sessions are
    # the conversation history Yoga wants visible from every account. claude
    # reads its usage over HTTP against the credential, so a shared sessions
    # dir costs nothing.
    SHARE_DEFAULT="skills plugins projects sessions session-env"
    COPY_FILES="settings.json"
    LOGIN_HINT='claude   # lalu /login di dalam REPL'
    ;;
  codex)
    ENV_VAR=CODEX_HOME
    SRC_DEFAULT="$HOME/.codex"
    DIR_FMT="$HOME/.codex_%s"
    # sessions is deliberately NOT here. codex has no usage endpoint: wick
    # reads the rate-limit windows out of $CODEX_HOME/sessions/**/rollout-*.jsonl
    # (internal/agents/provider/logintty/codex_usage.go), and the context
    # ledger and /compact read the same files. Share that dir and every
    # instance reports whichever account ran last.
    SHARE_DEFAULT="skills plugins rules"
    COPY_FILES="config.toml"
    LOGIN_HINT='codex login --device-auth   # link + one-time code, jalan headless'
    ;;
  gemini)
    warn "gemini belum bisa: wick meresolusi home-nya tanpa env override"
    warn "(logintty/spec.go → geminiConfigDir() tidak membaca Env instance),"
    warn "jadi dua instance gemini akan berbagi ~/.gemini apa pun isinya."
    exit 1 ;;
  *) warn "provider tidak dikenal: $KIND"; exit 1 ;;
esac

ask NAME "Nama home (dipakai jadi suffix folder + nama instance)?" "${ARG_NAME:-work}"
NAME=$(printf '%s' "$NAME" | tr -c 'A-Za-z0-9_' '_')
[ -n "$NAME" ] || { warn "nama kosong"; exit 1; }
# shellcheck disable=SC2059
DEST=$(printf "$DIR_FMT" "$NAME")

ask DEST "Folder home baru?" "$DEST"
ask SRC  "Mirror dari home mana?" "$SRC_DEFAULT"
[ -d "$SRC" ] || { warn "sumber tidak ada: $SRC"; exit 1; }

if [ -e "$DEST" ] && [ ! -d "$DEST" ]; then warn "$DEST ada tapi bukan folder"; exit 1; fi

# ---------------------------------------------------------------- what to share
say ""
note "Yang di-symlink = dipakai bareng semua akun (hemat, sekali edit kena semua)."
note "Yang tidak = milik akun ini sendiri (kredensial, history, kuota)."
ask SHARE "Symlink folder apa saja (pisah spasi, kosongkan = tidak ada)?" "$SHARE_DEFAULT"
ask COPY  "Salin file apa saja (salinan, bukan link)?" "$COPY_FILES"

# ---------------------------------------------------------------- plan
say ""
say "${c_b}Rencana${c_0}"
say "  home baru   : $DEST"
say "  sumber      : $SRC"
say "  symlink     : ${SHARE:-(tidak ada)}"
say "  salin       : ${COPY:-(tidak ada)}"
say "  env instance: $ENV_VAR=$DEST"
if [ "$KIND" = codex ]; then
  note "  catatan     : sessions/ TIDAK dibagi — di situ letak angka rate limit"
  note "                yang dibaca panel Usage tiap instance."
fi
say ""
confirm "Jalankan?" || { say "batal."; exit 0; }

# ---------------------------------------------------------------- do it
mkdir -p "$DEST"
chmod 700 "$DEST"

for d in $SHARE; do
  if [ ! -e "$SRC/$d" ]; then note "  lewati $d (tidak ada di sumber)"; continue; fi
  # A real (non-symlink) dir created by an earlier CLI run would swallow the
  # link INSIDE it — ln -sfn on a directory target links into it, not over it.
  if [ -d "$DEST/$d" ] && [ ! -L "$DEST/$d" ]; then
    rmdir "$DEST/$d" 2>/dev/null || { warn "  $DEST/$d sudah ada & tidak kosong — dilewati"; continue; }
  fi
  ln -sfn "$SRC/$d" "$DEST/$d"
  say "  link   $d -> $SRC/$d"
done

for f in $COPY; do
  if [ ! -f "$SRC/$f" ]; then note "  lewati $f (tidak ada di sumber)"; continue; fi
  if [ -e "$DEST/$f" ]; then note "  biarkan $f (sudah ada, tidak ditimpa)"; continue; fi
  cp "$SRC/$f" "$DEST/$f"
  say "  salin  $f"
done

# ---------------------------------------------------------------- next steps
say ""
say "${c_ok}Home siap:${c_0} $DEST"
say ""
say "${c_b}1. Login akun untuk home ini${c_0}"
say "   $ENV_VAR=$DEST $LOGIN_HINT"
say ""
say "${c_b}2. Daftarkan di wick${c_0}"
say "   Providers -> Add Instance -> type $KIND, name $NAME, Env:"
say "   $ENV_VAR=$DEST"
note "   Atau login langsung dari UI: buat instance-nya dulu dengan Env di atas,"
note "   lalu tombol Reconnect di panel Connection instance itu."
say ""
say "${c_b}3. Cek${c_0}"
if [ "$KIND" = codex ]; then
  say "   $ENV_VAR=$DEST codex login status"
else
  say "   ls -l $DEST/.credentials.json"
fi
say "   Kartu instance di Providers akan menampilkan email + plan akun ini sendiri."
say ""
