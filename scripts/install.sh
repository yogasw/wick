#!/usr/bin/env sh
# Universal installer — public/private repo aware. Detects OS + arch
# from `uname`, picks the matching release asset from GitHub, and
# installs to the platform-native location:
#   Termux  → $PREFIX/bin/<app>           (raw linux-arm64 binary)
#   Linux   → /usr/local/bin/<app>        (raw) or `dpkg -i` (.deb)
#   macOS   → /Applications/<app>.app     (mounts .dmg)
#   Windows → use install.ps1 instead
#
# Public repo:   curl -fsSL <url>/install.sh | sh
# Private repo:  TOKEN=ghp_xxx sh -c "$(curl -fsSL -H \"Authorization: Bearer $TOKEN\" <url>/install.sh)"
set -eu

APP="wick-agent"                 # auto-rewritten by `wick init`
REPO="yogasw/wick"               # auto-rewritten by `wick init` — EDIT after init
TOKEN="${TOKEN:-}"               # private repo → TOKEN=ghp_... at runtime

OS=$(uname -s)
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

AUTH=""
[ -n "$TOKEN" ] && AUTH="-H Authorization:Bearer $TOKEN"

# Privileged writes run plain — if the user lacks perms on the target
# dir (e.g. /usr/local/bin) the curl/mv/chmod call surfaces a clear
# error and the user can re-run with `sudo sh install.sh` themselves.
# Avoiding inline sudo saves a ~30s hostname-resolution stall on VMs
# where the host isn't in /etc/hosts, and matches the codex /
# rustup-style "you decide how to elevate" convention.

if [ "${VERSION:-latest}" = "latest" ]; then
  TAG=$(curl -fsSL $AUTH "https://api.github.com/repos/$REPO/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
else
  TAG="$VERSION"
fi
[ -z "$TAG" ] && { echo "could not resolve latest tag for $REPO" >&2; exit 1; }
VER="${TAG#v}"
BASE="https://github.com/$REPO/releases/download/$TAG"

# ── Pre-install version probe ────────────────────────────────────────
# Each component supports `<bin> version` (cobra-generated for the
# main app/gate; `gotty -v` for gotty). Failing probes return "" so
# the status table just shows "not installed". Skip flags are set
# when the installed version already matches the resolved TAG so we
# don't redo work on re-runs.
probe_version() {
  bin="$1"
  [ -n "$bin" ] && [ -x "$bin" ] || { echo ""; return; }
  v=$("$bin" version 2>/dev/null | head -1 | tr -d '\r')
  [ -n "$v" ] && { echo "$v"; return; }
  v=$("$bin" -v 2>/dev/null | head -1 | tr -d '\r')
  [ -n "$v" ] && { echo "$v"; return; }
  v=$("$bin" --version 2>/dev/null | head -1 | tr -d '\r')
  echo "$v"
}

APP_PATH=$(command -v "$APP" 2>/dev/null || echo "")
GATE_PATH=$(command -v "$APP-gate" 2>/dev/null || echo "")
GOTTY_PATH=$(command -v gotty 2>/dev/null || echo "")
APP_VER=$(probe_version "$APP_PATH")
GATE_VER=$(probe_version "$GATE_PATH")
GOTTY_VER=$(probe_version "$GOTTY_PATH")

SKIP_APP=0; SKIP_GATE=0
case "$APP_VER" in
  *"$TAG"*|*"$VER"*) SKIP_APP=1 ;;
esac
case "$GATE_VER" in
  *"$TAG"*|*"$VER"*) SKIP_GATE=1 ;;
esac

format_status() {
  cur="$1"; target="$2"
  if [ -z "$cur" ]; then
    echo "not installed    → install $target"
  elif echo "$cur" | grep -qF "$target"; then
    echo "$cur (up to date — skip)"
  else
    echo "$cur → upgrade to $target"
  fi
}

echo ""
echo "Component status (release: $TAG)"
printf "  %-18s : %s\n" "$APP"      "$(format_status "$APP_VER" "$TAG")"
printf "  %-18s : %s\n" "$APP-gate" "$(format_status "$GATE_VER" "$TAG")"
printf "  %-18s : %s\n" "gotty"     "${GOTTY_VER:-not installed} (prompt below)"
echo ""

# Helper: download the gate sidecar alongside the main binary. Gate is
# the PreToolUse hook the agent invokes before every Bash command — the
# .deb/.dmg/.msi bundle it implicitly, but raw installs need the
# explicit fetch.
install_gate() {
  dest_dir="$1"
  gate_url="$BASE/${APP}-gate-linux-${ARCH}"
  echo "→ gate: $gate_url"
  if curl -fsSL $AUTH "$gate_url" -o "$dest_dir/$APP-gate"; then
    chmod +x "$dest_dir/$APP-gate"
    echo "✓ $APP-gate installed at $dest_dir/$APP-gate"
  else
    echo "! gate sidecar not found at $gate_url — skipping (agent has an embedded fallback)"
  fi
}

# Helper: install gotty (sorenisanerd's maintained fork). Powers the
# Web Terminal feature. Optional — user is prompted Y/n. Pure-Go binary
# distributed as tarball, so no `go` toolchain required.
#
# Args: $1 dest_dir, $2 goos (linux|darwin)
# Uses outer-scope $ARCH (already normalised to amd64|arm64). Writes
# run plain — caller is expected to invoke install.sh with whatever
# privilege the target dir needs.
install_gotty() {
  dest_dir="$1"
  goos="$2"
  installed_ver=""
  if [ -x "$dest_dir/gotty" ]; then
    installed_ver=$("$dest_dir/gotty" -v 2>/dev/null | head -1 || true)
  fi
  # Resolve latest tag up-front so we can auto-skip when already current,
  # keeping re-runs config-only (no prompt, no download).
  gotty_tag=$(curl -fsSL "https://api.github.com/repos/sorenisanerd/gotty/releases/latest" \
              | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
  echo ""
  if [ -n "$installed_ver" ] && [ -n "$gotty_tag" ]; then
    gv="${gotty_tag#v}"
    if echo "$installed_ver" | grep -qF "$gv"; then
      echo "✓ gotty $installed_ver already at $gotty_tag — skipping"
      return 0
    fi
  fi
  if [ -n "$installed_ver" ]; then
    echo "gotty installed: $installed_ver (latest: ${gotty_tag:-unknown})"
  fi
  if [ -e /dev/tty ]; then
    if [ -n "$installed_ver" ]; then
      printf "Reinstall / upgrade gotty from https://github.com/sorenisanerd/gotty? [y/N]: "
      default_ans="n"
    else
      printf "Install gotty (web terminal) from https://github.com/sorenisanerd/gotty? [Y/n]: "
      default_ans="y"
    fi
    read ans < /dev/tty || ans=""
    [ -z "$ans" ] && ans="$default_ans"
  else
    echo "(no tty — skipping gotty install; rerun with a terminal to enable web terminal)"
    return 0
  fi
  case "$ans" in
    n|N|no|NO) echo "Skipped gotty install."; return 0 ;;
  esac
  if [ -z "$gotty_tag" ]; then
    echo "! could not resolve gotty latest tag — skipping" >&2
    return 0
  fi
  asset="gotty_${gotty_tag}_${goos}_${ARCH}.tar.gz"
  url="https://github.com/sorenisanerd/gotty/releases/download/${gotty_tag}/${asset}"
  tmp=$(mktemp -d)
  echo "→ gotty: $url"
  if ! curl -fsSL "$url" -o "$tmp/gotty.tar.gz"; then
    echo "! download failed — skipping gotty" >&2
    rm -rf "$tmp"
    return 0
  fi
  if ! tar -xzf "$tmp/gotty.tar.gz" -C "$tmp"; then
    echo "! extract failed — skipping gotty" >&2
    rm -rf "$tmp"
    return 0
  fi
  mv "$tmp/gotty" "$dest_dir/gotty"
  chmod +x "$dest_dir/gotty"
  rm -rf "$tmp"
  echo "✓ gotty $gotty_tag installed at $dest_dir/gotty"
}

# Termux first — $PREFIX with com.termux marker
if [ -n "${PREFIX:-}" ] && echo "$PREFIX" | grep -q 'com.termux'; then
  if [ "$SKIP_APP" = "1" ]; then
    echo "✓ $APP already at $TAG — skipping"
  else
    URL="$BASE/${APP}-linux-${ARCH}"
    echo "→ termux: $URL"
    curl -fsSL $AUTH "$URL" -o "$PREFIX/bin/$APP"
    chmod +x "$PREFIX/bin/$APP"
    echo "✓ $APP installed at $PREFIX/bin/$APP"
  fi
  if [ "$SKIP_GATE" = "1" ]; then
    echo "✓ $APP-gate already at $TAG — skipping"
  else
    install_gate "$PREFIX/bin"
  fi
  install_gotty "$PREFIX/bin" "linux"
  # LAN access prompt — Termux defaults to localhost, which is
  # unreachable from your laptop or another phone on the same Wi-Fi.
  # Surface every private-range IPv4 we can see and let the user pick
  # which to whitelist. We never silently expose — the phone might be
  # on public Wi-Fi where the agent UI shouldn't be reachable to every
  # device on the SSID.
  # Check for existing managed ALLOWED_ORIGINS in ~/.bashrc — re-runs
  # should be config-only, not force-reprompt every time.
  rc="$HOME/.bashrc"
  existing_origins=""
  if [ -f "$rc" ]; then
    existing_origins=$(grep -F "added by $APP installer" "$rc" 2>/dev/null \
                       | sed -n 's/^export ALLOWED_ORIGINS="\([^"]*\)".*/\1/p' | head -1)
  fi
  if [ -n "$existing_origins" ]; then
    echo ""
    echo "  LAN whitelist — existing ALLOWED_ORIGINS in $rc:"
    echo "    $existing_origins"
    if [ -e /dev/tty ]; then
      printf "  [k]eep / [e]dit / [c]lear [k]: "
      read wlchoice < /dev/tty || wlchoice=""
      [ -z "$wlchoice" ] && wlchoice="k"
    else
      wlchoice="k"
    fi
    case "$wlchoice" in
      k|K|keep|KEEP)
        echo "  ✓ keeping existing whitelist."
        exit 0
        ;;
      c|C|clear|CLEAR)
        tmp="$rc.tmp.$$"
        grep -vF "added by $APP installer" "$rc" > "$tmp" && mv "$tmp" "$rc"
        echo "  ✓ cleared whitelist from $rc."
        exit 0
        ;;
      e|E|edit|EDIT) ;;
      *) echo "  unknown choice — keeping existing."; exit 0 ;;
    esac
  fi

  if command -v ip >/dev/null 2>&1; then
    lan_ips=$(ip -4 addr show 2>/dev/null | awk '/inet (10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[0-1])\.)/ {print $2}' | cut -d/ -f1)
  else
    lan_ips=$(ifconfig 2>/dev/null | awk '/inet (10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[0-1])\.)/ {print $2}')
  fi
  if [ -n "$lan_ips" ]; then
    # Collect into a positional list — sh has no arrays, but `set --`
    # gives us $1..$N with stable indices for the prompt.
    set --
    for ip in $lan_ips; do set -- "$@" "$ip"; done
    n=$#
    echo ""
    echo "  LAN access — detected $n IPv4 address(es) on this device:"
    i=0
    for ip in "$@"; do
      i=$((i + 1))
      echo "    [$i] http://$ip:9425"
    done
    echo ""
    echo "  Whitelist for browser access from other devices?"
    echo "    a    = all       n = none (default)       1,2,3 = pick by number"
    # Reading from a terminal works when the script is piped (curl | sh)
    # only if /dev/tty is available — fall back to "n" otherwise.
    if [ -e /dev/tty ]; then
      printf "  Choice [n]: "
      read choice < /dev/tty || choice=""
    else
      choice=""
      echo "  (no tty — skipping; set ALLOWED_ORIGINS manually or use /admin/variables later)"
    fi
    selected=""
    case "$choice" in
      ""|n|N|no|NO) selected="" ;;
      a|A|all|ALL)  selected="$lan_ips" ;;
      *)
        # Parse comma-separated numbers, dedupe by index.
        for tok in $(echo "$choice" | tr ',' ' '); do
          case "$tok" in
            ''|*[!0-9]*) continue ;;
          esac
          if [ "$tok" -ge 1 ] && [ "$tok" -le "$n" ]; then
            eval "pick=\${$tok}"
            selected="$selected $pick"
          fi
        done
        ;;
    esac
    if [ -n "$selected" ]; then
      # Build comma-separated URL list for ALLOWED_ORIGINS.
      origins=""
      for ip in $selected; do
        url="http://$ip:9425"
        [ -z "$origins" ] && origins="$url" || origins="$origins,$url"
      done
      # Append (idempotently) to ~/.bashrc so every `$APP server` run
      # picks it up. We grep first to avoid stacking duplicates on
      # repeated installs.
      rc="$HOME/.bashrc"
      line="export ALLOWED_ORIGINS=\"$origins\"  # added by $APP installer"
      if [ -f "$rc" ] && grep -qF "added by $APP installer" "$rc" 2>/dev/null; then
        # Replace the existing managed line instead of appending again.
        tmp="$rc.tmp.$$"
        grep -vF "added by $APP installer" "$rc" > "$tmp" && mv "$tmp" "$rc"
      fi
      printf '\n%s\n' "$line" >> "$rc"
      echo "  ✓ added to $rc:"
      echo "    $line"
      echo "  Re-open your shell (or run: source $rc) before starting $APP."
    else
      echo "  Skipped — set ALLOWED_ORIGINS manually or add via /admin/variables later."
    fi
  fi
  exit 0
fi

case "$OS" in
  Darwin)
    if [ "$SKIP_APP" = "1" ]; then
      echo "✓ $APP already at $TAG — skipping"
    else
      URL="$BASE/${APP}-${VER}-darwin-${ARCH}.dmg"
      TMP=$(mktemp -d)
      echo "→ macOS: $URL"
      curl -fsSL $AUTH "$URL" -o "$TMP/$APP.dmg"
      hdiutil attach "$TMP/$APP.dmg" -nobrowse -quiet
      MOUNT=$(ls /Volumes | grep -i "$APP" | head -1)
      cp -R "/Volumes/$MOUNT/$APP.app" /Applications/
      hdiutil detach "/Volumes/$MOUNT" -quiet
      rm -rf "$TMP"
      echo "✓ $APP installed to /Applications/$APP.app"
    fi
    install_gotty "/usr/local/bin" "darwin"
    ;;
  Linux)
    if [ "$SKIP_APP" = "1" ] && [ "$SKIP_GATE" = "1" ]; then
      echo "✓ $APP and $APP-gate already at $TAG — skipping"
    elif command -v dpkg >/dev/null 2>&1; then
      if [ "$SKIP_APP" = "1" ]; then
        echo "✓ $APP already at $TAG — skipping .deb"
      else
        URL="$BASE/${APP}-${VER}-linux-${ARCH}.deb"
        TMP=$(mktemp)
        echo "→ linux: $URL"
        curl -fsSL $AUTH "$URL" -o "$TMP"
        dpkg -i "$TMP"
        rm -f "$TMP"
        echo "✓ $APP installed"
      fi
    else
      if [ "$SKIP_APP" = "1" ]; then
        echo "✓ $APP already at $TAG — skipping raw binary"
      else
        URL="$BASE/${APP}-linux-${ARCH}"
        echo "→ linux: $URL (raw, no dpkg)"
        curl -fsSL $AUTH "$URL" -o /usr/local/bin/$APP
        chmod +x /usr/local/bin/$APP
        echo "✓ $APP installed"
      fi
      if [ "$SKIP_GATE" = "1" ]; then
        echo "✓ $APP-gate already at $TAG — skipping"
      else
        # Gate sidecar — same idea as the Termux path. /usr/local/bin
        # is root-owned on most distros, so the caller must run
        # install.sh with root privileges (or via `sudo sh`).
        gate_url="$BASE/${APP}-gate-linux-${ARCH}"
        echo "→ gate: $gate_url"
        if curl -fsSL $AUTH "$gate_url" -o /usr/local/bin/$APP-gate; then
          chmod +x /usr/local/bin/$APP-gate
          echo "✓ $APP-gate installed at /usr/local/bin/$APP-gate"
        else
          echo "! gate sidecar not found at $gate_url — skipping (agent has an embedded fallback)"
        fi
      fi
    fi
    install_gotty "/usr/local/bin" "linux"
    ;;
  *)
    echo "unsupported OS: $OS (use install.ps1 for Windows)" >&2
    exit 1
    ;;
esac
