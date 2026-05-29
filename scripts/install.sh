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

# Termux first — $PREFIX with com.termux marker
if [ -n "${PREFIX:-}" ] && echo "$PREFIX" | grep -q 'com.termux'; then
  URL="$BASE/${APP}-linux-${ARCH}"
  echo "→ termux: $URL"
  curl -fsSL $AUTH "$URL" -o "$PREFIX/bin/$APP"
  chmod +x "$PREFIX/bin/$APP"
  echo "✓ $APP installed at $PREFIX/bin/$APP"
  install_gate "$PREFIX/bin"
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
    ;;
  *)
    echo "unsupported OS: $OS (use install.ps1 for Windows)" >&2
    exit 1
    ;;
esac
