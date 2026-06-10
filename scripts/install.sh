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
# Override app:  APP=myapp REPO=org/myapp TOKEN=ghp_xxx sh -c "$(curl -fsSL <url>/install.sh)"
set -eu

APP="${APP:-wick-agent}"          # override: APP=myapp curl ... | sh
REPO="${REPO:-yogasw/wick}"      # override: REPO=owner/myapp curl ... | sh
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
  echo "→ resolving latest tag for $REPO..."
  TAG=$(curl -fsSL --max-time 15 $AUTH "https://api.github.com/repos/$REPO/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
else
  TAG="$VERSION"
fi
[ -z "$TAG" ] && { echo "could not resolve latest tag for $REPO" >&2; exit 1; }
VER="${TAG#v}"
BASE="https://github.com/$REPO/releases/download/$TAG"

# ── Pre-install version probe ────────────────────────────────────────
# Three subtle bugs we need to avoid here:
#  1. Probed binaries inherit sh's stdin (the `curl | sh` pipe). If a
#     probe reads stdin (e.g. wick-agent-gate, which expects PreToolUse
#     hook JSON), it steals lines from the script body and sh later
#     dies with "Syntax error: end of file unexpected (expecting fi)".
#     Cure: redirect </dev/null on every probe call.
#  2. `gotty version` is not a version subcommand — gotty interprets
#     it as "serve the command `version` over a web terminal" and binds
#     :8080, which fails (port in use) or hangs (port free). Skip the
#     `version` subcommand attempt for gotty.
#  3. gate has no `version` subcommand or `--version` flag — every
#     probe path returns hook-error JSON. Just inherit APP_VER (gate
#     ships in the same release/.deb/.msi as the main app).
probe_version() {
  bin="$1"
  [ -n "$bin" ] && [ -x "$bin" ] || { echo ""; return; }
  v=$("$bin" --version </dev/null 2>/dev/null | head -1 | tr -d '\r')
  [ -n "$v" ] && { echo "$v"; return; }
  v=$("$bin" -v </dev/null 2>/dev/null | head -1 | tr -d '\r')
  [ -n "$v" ] && { echo "$v"; return; }
  v=$("$bin" version </dev/null 2>/dev/null | head -1 | tr -d '\r')
  echo "$v"
}

# gotty-specific: only --version is safe; `version` and `-v` need
# special handling (the latter is the correct flag but we still want to
# avoid the `version` subcommand fallback that probe_version would try).
probe_gotty() {
  bin="$1"
  [ -n "$bin" ] && [ -x "$bin" ] || { echo ""; return; }
  v=$("$bin" --version </dev/null 2>/dev/null | head -1 | tr -d '\r')
  echo "$v"
}

APP_PATH=$(command -v "$APP" 2>/dev/null || echo "")
GATE_PATH=$(command -v "$APP-gate" 2>/dev/null || echo "")
GOTTY_PATH=$(command -v gotty 2>/dev/null || echo "")
APP_VER=$(probe_version "$APP_PATH")
# Gate ships alongside app in the same release; probing gate directly
# is unsafe (see comment above), so reuse APP_VER when the gate binary
# is present.
if [ -n "$GATE_PATH" ]; then
  GATE_VER="$APP_VER"
else
  GATE_VER=""
fi
GOTTY_VER=$(probe_gotty "$GOTTY_PATH")

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
printf "  %-18s : %s\n" "gotty"     "${GOTTY_VER:-not installed} (auto-install/upgrade)"
echo ""

# Helper: stop the running agent before overwriting its binary. curl -o
# truncates the target first, which fails with "Text file busy" on
# Linux/Termux if the binary is currently executing. `wick-agent stop`
# kills the daemon cleanly; ignore failure (binary may be stale/broken).
# Gate is a per-invocation hook, not a daemon — no stop needed.
stop_running() {
  bin="$1"
  [ -x "$bin" ] || return 0
  "$bin" stop </dev/null >/dev/null 2>&1 || true
}

# Helper: download the gate sidecar alongside the main binary. Gate is
# the PreToolUse hook the agent invokes before every Bash command — the
# .deb/.dmg/.msi bundle it implicitly, but raw installs need the
# explicit fetch.
install_gate() {
  dest_dir="$1"
  gate_url="$BASE/${APP}-gate-linux-${ARCH}"
  echo "→ gate: $gate_url"
  if curl -fL --progress-bar $AUTH "$gate_url" -o "$dest_dir/$APP-gate"; then
    chmod +x "$dest_dir/$APP-gate"
    echo "✓ $APP-gate installed at $dest_dir/$APP-gate"
  else
    echo "! gate sidecar not found at $gate_url — skipping (agent has an embedded fallback)"
  fi
}

# Helper: install gotty (sorenisanerd's maintained fork). Powers the
# Web Terminal feature. Pure-Go binary distributed as tarball, so no
# `go` toolchain required.
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
    # </dev/null so gotty doesn't steal sh's stdin (see probe_version).
    installed_ver=$("$dest_dir/gotty" --version </dev/null 2>/dev/null | head -1 || true)
  fi
  # Resolve latest tag up-front so we can auto-skip when already current,
  # keeping re-runs config-only (no prompt, no download).
  echo "→ resolving latest gotty tag..."
  gotty_tag=$(curl -fsSL --max-time 15 "https://api.github.com/repos/sorenisanerd/gotty/releases/latest" \
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
    echo "→ gotty installed: $installed_ver (latest: ${gotty_tag:-unknown}) — upgrading automatically"
  else
    echo "→ installing gotty automatically"
  fi
  if [ -z "$gotty_tag" ]; then
    echo "! could not resolve gotty latest tag — skipping" >&2
    return 0
  fi
  asset="gotty_${gotty_tag}_${goos}_${ARCH}.tar.gz"
  url="https://github.com/sorenisanerd/gotty/releases/download/${gotty_tag}/${asset}"
  tmp=$(mktemp -d)
  echo "→ gotty: $url"
  if ! curl -fL --progress-bar "$url" -o "$tmp/gotty.tar.gz"; then
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

# Helper: spawn the agent after install. `start` is idempotent: it is a
# no-op when an existing daemon is already alive, and otherwise detaches
# into tray/all mode depending on the host.
start_agent() {
  bin="$1"
  [ -n "$bin" ] && [ -x "$bin" ] || bin=$(command -v "$APP" 2>/dev/null || echo "")
  if [ -z "$bin" ] || [ ! -x "$bin" ]; then
    echo "! could not find $APP on PATH — skipping auto-start" >&2
    return 0
  fi
  echo "→ starting $APP..."
  if "$bin" start </dev/null; then
    echo "✓ $APP started"
  else
    echo "! $APP start failed — install completed, run '$bin start' manually to retry" >&2
  fi
}

# Termux first — $PREFIX with com.termux marker
if [ -n "${PREFIX:-}" ] && echo "$PREFIX" | grep -q 'com.termux'; then
  if [ "$SKIP_APP" = "1" ]; then
    echo "✓ $APP already at $TAG — skipping"
  else
    URL="$BASE/${APP}-linux-${ARCH}"
    echo "→ termux: $URL"
    stop_running "$PREFIX/bin/$APP"
    curl -fL --progress-bar $AUTH "$URL" -o "$PREFIX/bin/$APP"
    chmod +x "$PREFIX/bin/$APP"
    echo "✓ $APP installed at $PREFIX/bin/$APP"
  fi
  if [ "$SKIP_GATE" = "1" ]; then
    echo "✓ $APP-gate already at $TAG — skipping"
  else
    install_gate "$PREFIX/bin"
  fi
  install_gotty "$PREFIX/bin" "linux"

  # ── proot — Termux prerequisite ───────────────────────────────────
  # proot lets us bind-mount Termux's real DNS + CA bundle into the
  # /etc paths that musl-linked binaries (codex, and other upstream
  # CLIs distributed as static linux-arm64) hard-code. Install it
  # unconditionally on Termux so the binding is available the first
  # time any such tool is run — not only when codex happens to already
  # be on PATH at install time.
  if ! command -v proot >/dev/null 2>&1; then
    echo ""
    echo "→ installing proot (Termux prerequisite)…"
    if ! pkg install -y proot >/dev/null 2>&1; then
      echo "! pkg install proot failed — install it manually with: pkg install proot" >&2
    fi
  fi

  # ── Codex CLI fix for Termux ──────────────────────────────────────
  # OpenAI's Codex CLI ships a musl-linked binary that hard-codes:
  #   /etc/resolv.conf              (DNS)
  #   /etc/ssl/certs/ca-certificates.crt  (TLS roots)
  # Android's /etc is read-only, so `codex login --device-auth` fails
  # with: "error sending request for url …auth.openai.com…".
  # Termux keeps the real files at $PREFIX/etc/resolv.conf and
  # $PREFIX/etc/tls/cert.pem — proot lets us bind-mount them into the
  # paths codex expects without modifying the binary or the system.
  #
  # Workaround source: https://gist.github.com/netanel-haber/77b91c4148249394d75546348bae7698
  codex_rc="$HOME/.bashrc"
  codex_marker="# $APP installer: codex-termux fix"
  if command -v codex >/dev/null 2>&1; then
    if [ -f "$codex_rc" ] && grep -qF "$codex_marker" "$codex_rc" 2>/dev/null; then
      echo ""
      echo "✓ codex termux alias already in $codex_rc — skipping"
    else
      echo ""
      echo "→ applying codex termux fix (proot bind-mount for DNS + CA bundle)"
      alias_line="alias codex='proot -b \$PREFIX/etc/resolv.conf:/etc/resolv.conf -b \$PREFIX/etc/tls/cert.pem:/etc/ssl/certs/ca-certificates.crt codex'  $codex_marker"
      printf '\n%s\n' "$alias_line" >> "$codex_rc"
      echo "  ✓ added to $codex_rc:"
      echo "    $alias_line"
      echo "  Re-open your shell (or run: source $codex_rc) before calling codex."
    fi
  fi
  
  start_agent "$PREFIX/bin/$APP"
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
      curl -fL --progress-bar $AUTH "$URL" -o "$TMP/$APP.dmg"
      hdiutil attach "$TMP/$APP.dmg" -nobrowse -quiet
      MOUNT=$(ls /Volumes | grep -i "$APP" | head -1)
      cp -R "/Volumes/$MOUNT/$APP.app" /Applications/
      hdiutil detach "/Volumes/$MOUNT" -quiet
      rm -rf "$TMP"
      echo "✓ $APP installed to /Applications/$APP.app"
    fi
    install_gotty "/usr/local/bin" "darwin"
    start_agent "/Applications/$APP.app/Contents/MacOS/$APP"
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
        curl -fL --progress-bar $AUTH "$URL" -o "$TMP"
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
        stop_running "/usr/local/bin/$APP"
        curl -fL --progress-bar $AUTH "$URL" -o /usr/local/bin/$APP
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
        if curl -fL --progress-bar $AUTH "$gate_url" -o /usr/local/bin/$APP-gate; then
          chmod +x /usr/local/bin/$APP-gate
          echo "✓ $APP-gate installed at /usr/local/bin/$APP-gate"
        else
          echo "! gate sidecar not found at $gate_url — skipping (agent has an embedded fallback)"
        fi
      fi
    fi
    install_gotty "/usr/local/bin" "linux"
    start_agent "$(command -v "$APP" 2>/dev/null || echo "/usr/local/bin/$APP")"
    ;;
  *)
    echo "unsupported OS: $OS (use install.ps1 for Windows)" >&2
    exit 1
    ;;
esac
