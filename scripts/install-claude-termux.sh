#!/data/data/com.termux/files/usr/bin/bash
# Install Claude Code on Termux (aarch64).
#
# Upstream's installer assumes glibc + a writable /etc, neither of which
# Termux provides. This script:
#   1. Downloads the linux-arm64 Claude binary directly
#   2. Patches its ELF interpreter to point at Termux's glibc-runner ld.so
#      so the kernel can exec it (required because Claude's embedded
#      grep/find re-execs preserving argv[0], which only survives a
#      kernel-direct exec — running under `grun` breaks that path).
#   3. Writes a small wrapper at ~/.local/bin/claude that unsets
#      LD_PRELOAD before the exec so termux-exec doesn't crash on
#      Claude's glibc-linked libc.
#
# Source: https://github.com/anthropics/claude-code/issues/50270#issuecomment-4584920515
# Credit: upstream community workaround. We host a copy so the docs stay
# stable when the original GitHub comment is edited.
set -euo pipefail

BINARY=~/.local/share/claude-code/claude
WRAPPER=~/.local/bin/claude
BASE_URL=https://downloads.claude.ai/claude-code-releases
GLIBC_LD=/data/data/com.termux/files/usr/glibc/lib/ld-linux-aarch64.so.1
PATCHELF=/data/data/com.termux/files/usr/glibc/bin/patchelf
SETTINGS=~/.claude/settings.json

# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

DEPS=(curl jq glibc-runner patchelf-glibc)
MISSING=()

for dep in "${DEPS[@]}"; do
  if ! dpkg -s "$dep" &>/dev/null; then
    MISSING+=("$dep")
  fi
done

if ! dpkg -s glibc-repo &>/dev/null; then
  need_repo=false
  for dep in "${MISSING[@]}"; do
    if [[ "$dep" == "glibc-runner" || "$dep" == "patchelf-glibc" ]]; then
      need_repo=true
      break
    fi
  done
  if $need_repo; then
    MISSING=(glibc-repo "${MISSING[@]}")
  fi
fi

if [ ${#MISSING[@]} -gt 0 ]; then
  echo "The following packages are required but not installed:"
  echo ""
  for dep in "${MISSING[@]}"; do
    echo "  - $dep"
  done
  echo ""
  read -rp "Install them now? [Y/n] " ans
  if [[ "$ans" =~ ^[Nn] ]]; then
    echo "Cannot continue without dependencies." >&2
    exit 1
  fi

  if [[ " ${MISSING[*]} " =~ " glibc-repo " ]]; then
    apt install -y glibc-repo
    remaining=()
    for dep in "${MISSING[@]}"; do
      [ "$dep" != "glibc-repo" ] && remaining+=("$dep")
    done
    MISSING=("${remaining[@]}")
  fi

  if [ ${#MISSING[@]} -gt 0 ]; then
    apt install -y "${MISSING[@]}"
  fi

  echo ""
  echo "Dependencies installed."
fi

# ---------------------------------------------------------------------------
# Version check
# ---------------------------------------------------------------------------

LATEST=$(curl -fsSL "$BASE_URL/latest")
if ! [[ "$LATEST" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Failed to fetch latest version (got: $LATEST)" >&2
  exit 1
fi
CURRENT=$("$WRAPPER" --version 2>/dev/null | awk '{print $1}' || echo "none")

if [ "$CURRENT" = "$LATEST" ] && [ $# -eq 0 ]; then
  echo "Already on latest: $CURRENT"
  exit 0
fi

VERSION="${1:-$LATEST}"
DL="$BASE_URL/$VERSION"

if [ "$CURRENT" = "none" ]; then
  echo "Installing Claude Code $VERSION ..."
else
  echo "Updating: $CURRENT -> $VERSION"
fi

# ---------------------------------------------------------------------------
# Download & verify
# ---------------------------------------------------------------------------

mkdir -p "$(dirname "$BINARY")" "$(dirname "$WRAPPER")"

TMPBIN=$(mktemp)
trap 'rm -f "$TMPBIN"' EXIT INT TERM

curl -fSL "$DL/linux-arm64/claude" -o "$TMPBIN"

expected=$(curl -fsSL "$DL/manifest.json" | jq -er '.platforms["linux-arm64"].checksum')
actual=$(sha256sum "$TMPBIN" | cut -d' ' -f1)
if [ "$actual" != "$expected" ]; then
  echo "Checksum mismatch: $actual != $expected" >&2
  exit 1
fi

mv "$TMPBIN" "$BINARY"
trap - EXIT INT TERM
chmod +x "$BINARY"

# ---------------------------------------------------------------------------
# Patchelf — make the kernel exec the binary directly under glibc-runner's
# ld.so. argv[0]-preserving re-exec inside Claude only works via kernel
# exec, not via `grun`. LD_PRELOAD= here keeps termux-exec from crashing
# patchelf itself on the glibc path.
# ---------------------------------------------------------------------------

LD_PRELOAD= "$PATCHELF" --set-interpreter "$GLIBC_LD" "$BINARY"

# ---------------------------------------------------------------------------
# Wrapper script — unsets LD_PRELOAD so termux-exec doesn't crash on
# Claude's glibc-linked libc.
# ---------------------------------------------------------------------------

cat > "$WRAPPER" <<EOF
#!/data/data/com.termux/files/usr/bin/bash
unset LD_PRELOAD
exec "$BINARY" "\$@"
EOF
chmod +x "$WRAPPER"

# ---------------------------------------------------------------------------
# Settings — disable autoUpdate (must use this script for upgrades) and
# clean up stale LD_PRELOAD workarounds from older installs.
# ---------------------------------------------------------------------------

mkdir -p "$(dirname "$SETTINGS")"

if [ ! -f "$SETTINGS" ]; then
  printf '{\n  "autoUpdates": false\n}\n' > "$SETTINGS"
  echo "Created $SETTINGS"
elif ! jq empty "$SETTINGS" 2>/dev/null; then
  echo "Warning: $SETTINGS contains invalid JSON — skipping settings update."
else
  changed=false
  tmp=$(mktemp)
  cp "$SETTINGS" "$tmp"

  if [ "$(jq '.autoUpdates' "$tmp")" != "false" ]; then
    jq '.autoUpdates = false' "$tmp" > "${tmp}.2" && mv "${tmp}.2" "$tmp"
    changed=true
  fi

  if [ "$(jq 'has("env") and (.env | has("LD_PRELOAD"))' "$tmp")" = "true" ]; then
    jq 'del(.env.LD_PRELOAD)' "$tmp" > "${tmp}.2" && mv "${tmp}.2" "$tmp"
    changed=true
  fi

  if [ "$changed" = true ]; then
    mv "$tmp" "$SETTINGS"
    echo "Updated $SETTINGS (set autoUpdates, cleaned up old LD_PRELOAD workarounds)"
  else
    rm -f "$tmp"
    echo "Settings already configured."
  fi
fi

# ---------------------------------------------------------------------------
# Shell RC — ensure ~/.local/bin is on PATH
# ---------------------------------------------------------------------------

WRAPPER_DIR="$(dirname "$WRAPPER")"
PATH_LINE='export PATH="$HOME/.local/bin:$PATH"'

add_to_rc() {
  local rc="$1"
  if [ -f "$rc" ] && grep -qF '/.local/bin' "$rc"; then
    echo "PATH already configured in $rc"
  else
    echo "" >> "$rc"
    echo "$PATH_LINE" >> "$rc"
    echo "Added ~/.local/bin to PATH in $rc"
    echo "Run 'source $rc' or restart your shell to apply."
  fi
}

case "$(basename "$SHELL")" in
  zsh)  add_to_rc ~/.zshrc ;;
  bash) add_to_rc ~/.bashrc ;;
  *)
    if [ -f ~/.profile ]; then
      add_to_rc ~/.profile
    else
      echo ""
      echo "Could not detect your shell RC file."
      echo "Add this line manually:"
      echo ""
      echo "  $PATH_LINE"
    fi
    ;;
esac

case ":$PATH:" in
  *:"$WRAPPER_DIR":*) ;;
  *) export PATH="$WRAPPER_DIR:$PATH" ;;
esac

# ---------------------------------------------------------------------------
# Check for old npm-installed Claude Code — npm version conflicts with
# the native binary because both respond to `claude`.
# ---------------------------------------------------------------------------

NPM_CLAUDE=""
while IFS= read -r p; do
  real="$(readlink -f "$p" 2>/dev/null || echo "$p")"
  if [ "$real" != "$(readlink -f "$WRAPPER" 2>/dev/null)" ] && \
     [ "$real" != "$(readlink -f "$BINARY" 2>/dev/null)" ]; then
    NPM_CLAUDE="$p"
    break
  fi
done < <(which -a claude 2>/dev/null || true)

if [ -n "$NPM_CLAUDE" ]; then
  NPM_DIR="$(dirname "$NPM_CLAUDE")"
  echo ""
  echo "=========================================="
  echo "  Old Claude Code installation detected"
  echo "=========================================="
  echo ""
  echo "  Location: $NPM_CLAUDE"
  echo ""
  echo "  The native binary has been installed, but the old"
  echo "  version is still on your PATH and may take priority"
  echo "  or cause confusion."
  echo ""
  echo "  What would you like to do?"
  echo ""
  echo "  1) Rename it  — keep the old binary under a different name"
  echo "  2) Uninstall  — remove the npm package entirely"
  echo "  3) Do nothing — leave it as-is"
  echo ""
  read -rp "  Choose [1/2/3]: " choice

  case "$choice" in
    1)
      read -rp "  Enter new name for the old binary (e.g. claudo): " newname
      if [ -z "$newname" ]; then
        echo "  No name entered, skipping."
      elif [ -e "$NPM_DIR/$newname" ]; then
        echo "  $NPM_DIR/$newname already exists, skipping."
      else
        mv "$NPM_CLAUDE" "$NPM_DIR/$newname"
        echo ""
        echo "  Renamed: $NPM_CLAUDE -> $NPM_DIR/$newname"
        echo "  You can still use the old version as '$newname'."
      fi
      ;;
    2)
      echo ""
      echo "  Uninstalling npm Claude Code ..."
      npm uninstall -g @anthropic-ai/claude-code
      echo "  Done."
      ;;
    3)
      echo ""
      echo "  Leaving old installation in place."
      echo ""
      echo "  Note: both versions respond to the 'claude' command."
      echo "  Which one runs depends on PATH order. Currently:"
      echo "    $(which claude 2>/dev/null || echo '(not found)')"
      echo "  If that's not the native version, reorder your PATH"
      echo "  so that ~/.local/bin comes before $NPM_DIR."
      ;;
    *)
      echo "  Invalid choice, skipping."
      ;;
  esac
fi

echo ""
echo "Claude Code $VERSION is ready. Run 'claude' to start."
