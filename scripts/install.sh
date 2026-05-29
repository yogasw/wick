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

if [ "${VERSION:-latest}" = "latest" ]; then
  TAG=$(curl -fsSL $AUTH "https://api.github.com/repos/$REPO/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
else
  TAG="$VERSION"
fi
[ -z "$TAG" ] && { echo "could not resolve latest tag for $REPO" >&2; exit 1; }
VER="${TAG#v}"
BASE="https://github.com/$REPO/releases/download/$TAG"

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
# Uses outer-scope $ARCH (already normalised to amd64|arm64).
install_gotty() {
  dest_dir="$1"
  goos="$2"
  prompt="$3"   # "sudo" → use sudo for curl/mv/chmod; "" → run plain.
  echo ""
  if [ -e /dev/tty ]; then
    printf "Install gotty (web terminal) from https://github.com/sorenisanerd/gotty? [Y/n]: "
    read ans < /dev/tty || ans=""
  else
    ans=""
    echo "(no tty — skipping gotty install; rerun with a terminal to enable web terminal)"
    return 0
  fi
  case "$ans" in
    n|N|no|NO) echo "Skipped gotty install."; return 0 ;;
  esac
  gotty_tag=$(curl -fsSL "https://api.github.com/repos/sorenisanerd/gotty/releases/latest" \
              | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
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
  if [ "$prompt" = "sudo" ]; then
    sudo mv "$tmp/gotty" "$dest_dir/gotty"
    sudo chmod +x "$dest_dir/gotty"
  else
    mv "$tmp/gotty" "$dest_dir/gotty"
    chmod +x "$dest_dir/gotty"
  fi
  rm -rf "$tmp"
  echo "✓ gotty $gotty_tag installed at $dest_dir/gotty"
}

# Termux first — $PREFIX with com.termux marker
if [ -n "${PREFIX:-}" ] && echo "$PREFIX" | grep -q 'com.termux'; then
  URL="$BASE/${APP}-linux-${ARCH}"
  echo "→ termux: $URL"
  curl -fsSL $AUTH "$URL" -o "$PREFIX/bin/$APP"
  chmod +x "$PREFIX/bin/$APP"
  echo "✓ $APP installed at $PREFIX/bin/$APP"
  install_gate "$PREFIX/bin"
  install_gotty "$PREFIX/bin" "linux" ""
  # LAN access prompt — Termux defaults to localhost, which is
  # unreachable from your laptop or another phone on the same Wi-Fi.
  # Surface every private-range IPv4 we can see and let the user pick
  # which to whitelist. We never silently expose — the phone might be
  # on public Wi-Fi where the agent UI shouldn't be reachable to every
  # device on the SSID.
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
    install_gotty "/usr/local/bin" "darwin" "sudo"
    ;;
  Linux)
    if command -v dpkg >/dev/null 2>&1; then
      URL="$BASE/${APP}-${VER}-linux-${ARCH}.deb"
      TMP=$(mktemp)
      echo "→ linux: $URL"
      curl -fsSL $AUTH "$URL" -o "$TMP"
      sudo dpkg -i "$TMP"
      rm -f "$TMP"
    else
      URL="$BASE/${APP}-linux-${ARCH}"
      echo "→ linux: $URL (raw, no dpkg)"
      sudo curl -fsSL $AUTH "$URL" -o /usr/local/bin/$APP
      sudo chmod +x /usr/local/bin/$APP
      # Gate sidecar — same idea as the Termux path. Need sudo here
      # because /usr/local/bin is root-owned on most distros.
      gate_url="$BASE/${APP}-gate-linux-${ARCH}"
      echo "→ gate: $gate_url"
      if sudo curl -fsSL $AUTH "$gate_url" -o /usr/local/bin/$APP-gate; then
        sudo chmod +x /usr/local/bin/$APP-gate
        echo "✓ $APP-gate installed at /usr/local/bin/$APP-gate"
      else
        echo "! gate sidecar not found at $gate_url — skipping (agent has an embedded fallback)"
      fi
    fi
    echo "✓ $APP installed"
    install_gotty "/usr/local/bin" "linux" "sudo"
    ;;
  *)
    echo "unsupported OS: $OS (use install.ps1 for Windows)" >&2
    exit 1
    ;;
esac
