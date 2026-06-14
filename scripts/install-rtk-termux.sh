#!/bin/sh
# rtk install & auto-update script (Termux/Android)
# Usage: sh install-rtk-termux.sh
#        sh install-rtk-termux.sh --force

set -e

REPO="rtk-ai/rtk"
TARGET="aarch64-unknown-linux-gnu"
STORE_DIR="$HOME/.local/share/rtk"
STORE_BIN="$STORE_DIR/rtk"
WRAPPER="$HOME/.local/bin/rtk"
FORCE=0

for arg in "$@"; do
  case "$arg" in
    --force|-f) FORCE=1 ;;
  esac
done

if ! command -v grun >/dev/null 2>&1; then
  echo "ERROR: grun not found — install glibc-runner first"
  echo "Fix: curl -fsSL https://yogasw.github.io/wick/install-claude-termux.sh | bash"
  exit 1
fi

latest_version() {
  VERSION=$(curl -fsSLI -o /dev/null -w "%{url_effective}" \
    "https://github.com/$REPO/releases/latest" 2>/dev/null \
    | sed 's|.*/tag/||')
  if [ -z "$VERSION" ] || echo "$VERSION" | grep -q "releases/latest"; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
      | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
  fi
  echo "$VERSION"
}

current_version() {
  if [ -x "$WRAPPER" ]; then
    "$WRAPPER" --version 2>/dev/null | awk '{print $NF}' | head -1
  else
    echo "none"
  fi
}

install_rtk() {
  LATEST=$(latest_version)
  CURRENT=$(current_version)
  LATEST_CLEAN=$(echo "$LATEST" | sed 's/^v//')
  CURRENT_CLEAN=$(echo "$CURRENT" | sed 's/^v//')
  echo "rtk: current=${CURRENT:-none}  latest=$LATEST"
  if [ "$CURRENT_CLEAN" = "$LATEST_CLEAN" ] && [ "$FORCE" -eq 0 ]; then
    echo "rtk is already up to date ($CURRENT)."
    return 0
  fi
  ASSET="rtk-${TARGET}.tar.gz"
  URL="https://github.com/$REPO/releases/download/$LATEST/$ASSET"
  TMPDIR=$(mktemp -d)
  ARCHIVE="$TMPDIR/$ASSET"
  echo "Downloading $URL ..."
  curl -fsSL "$URL" -o "$ARCHIVE"
  echo "Extracting ..."
  tar -xzf "$ARCHIVE" -C "$TMPDIR"
  EXTRACTED=$(find "$TMPDIR" -type f -name "rtk" | head -1)
  if [ -z "$EXTRACTED" ]; then
    echo "ERROR: rtk binary not found in archive"
    rm -rf "$TMPDIR"
    exit 1
  fi
  mkdir -p "$STORE_DIR" "$HOME/.local/bin"
  cp "$EXTRACTED" "$STORE_BIN"
  chmod +x "$STORE_BIN"
  rm -rf "$TMPDIR"
  cat > "$WRAPPER" <<EOF
#!/bin/sh
exec grun $STORE_BIN "\$@"
EOF
  chmod +x "$WRAPPER"
  echo "rtk $LATEST installed."
}

ensure_path_hint() {
  case ":$PATH:" in
    *":$HOME/.local/bin:"*) ;;
    *)
      echo ""
      echo "NOTE: ~/.local/bin is not in your PATH."
      echo "Add to ~/.bashrc or ~/.zshrc:"
      echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
      ;;
  esac
}

install_rtk
ensure_path_hint
