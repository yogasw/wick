#!/usr/bin/env bash
# deploy-from-source.sh — build a wick app (and its plugins) from source on Linux or
# macOS and hot-reload it with no downtime. Installs what the build needs (git, Go,
# the wick CLI) when missing. Windows: deploy-from-source.ps1.
#
#   curl -fsSL https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source.sh \
#     | bash -s -- all --git https://github.com/org/myapp.git
#
# See https://yogasw.github.io/wick/guide/deploy-from-source for the full guide.
set -euo pipefail

usage() { cat <<'EOF'
Usage: deploy-from-source.sh <command> [flags] [plugin-key...]

Commands
  setup     install git, Go (the version the app's go.mod asks for) and the wick CLI
  core      build the app binary and swap it in: zero-downtime reload when the app is
            running, a fresh .deb install when it is not installed yet
  plugins   build + install plugins under plugins/{connector,tool,job,service}/<key>
            (default: only those whose VERSION is newer than the installed one, or new)
  all       setup, core, then plugins
  status    app version and installed plugins

Flags
  --dir DIR        app checkout (default: current dir if it has wick.yml, else ~/<repo name>)
  --git URL        clone this repo into --dir when it is not there yet (https recommended;
                   private GitHub repo: set TOKEN=ghp_... in the environment)
  --branch NAME    branch to clone / pull (default: the repo's default branch)
  --app NAME       installed binary name (default: name: in wick.yml)
  --wick DIR       build against a local wick checkout (temporary go.mod replace, always restored)
  --no-upgrade     keep the wick version in go.mod (default: `wick upgrade -y` to the latest release)
  --no-pull        do not git pull
  --no-bump        keep wick.yml's version (default: bump the patch so reload accepts the build)
  --no-test        skip go test before building
  --all-plugins    rebuild every plugin, not only the newer ones
  --bump-plugins   bump each built plugin's VERSION patch first
  --restart        install with stop / swap / start instead of the zero-downtime reload
                   (a daemon started without WICK_GRACEFUL_UPGRADE=1; has a short downtime)
  --dry-run        print what would run, change nothing
EOF
}

CMD="${1:-}"; [ $# -gt 0 ] && shift
DIR=""; GIT_URL=""; BRANCH=""; APP=""; WICK_DIR=""
UPGRADE=1; PULL=1; BUMP=1; TEST=1; ALL_PLUGINS=0; BUMP_PLUGINS=0; DRY=0; RESTART=0; KEYS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --dir) DIR="$2"; shift 2 ;;
    --git) GIT_URL="$2"; shift 2 ;;
    --branch) BRANCH="$2"; shift 2 ;;
    --app) APP="$2"; shift 2 ;;
    --wick) WICK_DIR="$(cd "$2" && pwd)"; shift 2 ;;
    --restart) RESTART=1; shift ;;
    --no-upgrade) UPGRADE=0; shift ;;
    --no-pull) PULL=0; shift ;;
    --no-bump) BUMP=0; shift ;;
    --no-test) TEST=0; shift ;;
    --all-plugins) ALL_PLUGINS=1; shift ;;
    --bump-plugins) BUMP_PLUGINS=1; shift ;;
    --dry-run) DRY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
    *) KEYS+=("$1"); shift ;;
  esac
done

say() { printf '\n== %s\n' "$*"; }
die() { printf '!! %s\n' "$*" >&2; exit 1; }
run() { printf '   $ %s\n' "$*"; [ "$DRY" = 1 ] || "$@"; }
SUDO=""; [ "$(id -u)" = 0 ] || SUDO="sudo"
# heavy runs a build/test through buildq when the host has one (a shared-host build
# queue), else at low priority.
heavy() { local label=$1; shift
  if command -v buildq >/dev/null 2>&1; then run buildq -n "$label" -- "$@"; else run nice -n 10 "$@"; fi; }

case "$(uname -m)" in x86_64|amd64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) die "unsupported arch $(uname -m)" ;; esac
case "$(uname -s)" in Linux) OS=linux ;; Darwin) OS=darwin ;; *) die "unsupported OS $(uname -s) — on Windows use deploy-from-source.ps1" ;; esac
TARGET="$OS/$ARCH"
sedi() { local f=${*: -1}; sed -i.bak "$@" && rm -f "$f.bak"; }   # sed -i on GNU and BSD (macOS)
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

# git with TOKEN as a header, so the token never lands in .git/config or the process list.
git_auth() {
  if [ -n "${TOKEN:-}" ]; then
    git -c "http.extraHeader=Authorization: Basic $(printf 'x-access-token:%s' "$TOKEN" | base64 | tr -d '\n')" "$@"
  else git "$@"; fi
}

resolve_dir() {
  if [ -z "$DIR" ]; then
    if [ -f wick.yml ]; then DIR="$PWD"
    elif [ -n "$GIT_URL" ]; then DIR="$HOME/$(basename "${GIT_URL%.git}")"
    else die "no app here: run inside the app checkout, or pass --dir / --git"; fi
  fi
  if [ ! -d "$DIR/.git" ]; then
    [ -n "$GIT_URL" ] || die "$DIR is not a git checkout (pass --git URL to clone it)"
    say "clone $GIT_URL -> $DIR"
    run git_auth clone ${BRANCH:+--branch "$BRANCH"} "$GIT_URL" "$DIR"
    PULL=0
  fi
  [ "$DRY" = 1 ] && [ ! -d "$DIR" ] && return 0
  [ -f "$DIR/wick.yml" ] || die "$DIR has no wick.yml — not a wick app"
  [ -n "$APP" ] || APP="$(awk '/^name:/{print $2; exit}' "$DIR/wick.yml")"
  [ -n "$APP" ] || die "no name: in $DIR/wick.yml (pass --app)"
}

pull() {
  [ "$PULL" = 1 ] || return 0
  say "git pull"
  if [ -n "$BRANCH" ]; then run git_auth -C "$DIR" pull --ff-only origin "$BRANCH"; else run git_auth -C "$DIR" pull --ff-only; fi
  PULL=0
}

# ---------------------------------------------------------------- setup
need_pkgs() { # install missing OS packages (apt/dnf/apk, brew on macOS)
  local miss=() p; for p in "$@"; do command -v "$p" >/dev/null 2>&1 || miss+=("$p"); done
  [ ${#miss[@]} -gt 0 ] || return 0
  say "install ${miss[*]}"
  if [ "$OS" = darwin ]; then
    command -v brew >/dev/null || die "install ${miss[*]} (or Homebrew: https://brew.sh) first"
    run brew install "${miss[@]}"
  elif command -v apt-get >/dev/null; then run $SUDO apt-get update -qq; run $SUDO apt-get install -y -qq "${miss[@]}"
  elif command -v dnf >/dev/null; then run $SUDO dnf install -y "${miss[@]}"
  elif command -v apk >/dev/null; then run $SUDO apk add "${miss[@]}"
  else die "install ${miss[*]} by hand (no apt/dnf/apk)"; fi
}

go_wanted() { # the Go version the app's go.mod asks for (toolchain wins), else the latest
  local v=""
  if [ -f "$DIR/go.mod" ]; then
    v="$(awk '/^toolchain go/{sub("go","",$2); print $2; exit}' "$DIR/go.mod")"
    [ -n "$v" ] || v="$(awk '/^go [0-9]/{print $2; exit}' "$DIR/go.mod")"
  fi
  [ -n "$v" ] || v="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1 | sed 's/^go//')"
  echo "$v"
}

ver_ge() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$2" ]; }   # ver_ge A B: A >= B

setup() {
  need_pkgs git curl tar
  local want have=""; want="$(go_wanted)"
  command -v go >/dev/null 2>&1 && have="$(go env GOVERSION | sed 's/^go//')"
  if [ -z "$have" ] || ! ver_ge "$have" "$want"; then
    say "install Go $want (have: ${have:-none}) -> /usr/local/go"
    local tgz="go$want.$OS-$ARCH.tar.gz" tmp; tmp="$(mktemp -d)"
    run curl -fsSL -o "$tmp/$tgz" "https://go.dev/dl/$tgz"
    run $SUDO rm -rf /usr/local/go
    run $SUDO tar -C /usr/local -xzf "$tmp/$tgz"
    rm -rf "$tmp"
    grep -qs '/usr/local/go/bin' "$HOME/.profile" || [ "$DRY" = 1 ] || \
      echo 'export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"' >>"$HOME/.profile"
  else
    echo "   Go $have ok (need $want)"
  fi
  if ! command -v wick >/dev/null 2>&1; then
    # the CLI version the app already depends on, so `wick build` matches the module
    local wv=""; [ -f "$DIR/go.mod" ] && wv="$(awk '$1=="github.com/yogasw/wick"{print $2; exit}' "$DIR/go.mod")"
    say "install wick CLI ${wv:-latest}"
    run go install "github.com/yogasw/wick@${wv:-latest}"
  else
    echo "   wick CLI ok ($(command -v wick))"
  fi
}

# ---------------------------------------------------------------- core
GOMOD_SAVED=""
save_gomod()    { GOMOD_SAVED="$(mktemp -d)"; cp "$DIR/go.mod" "$DIR/go.sum" "$GOMOD_SAVED/" 2>/dev/null || true; }
restore_gomod() { [ -n "$GOMOD_SAVED" ] || return 0; cp "$GOMOD_SAVED"/go.* "$DIR/" 2>/dev/null || true; rm -rf "$GOMOD_SAVED"; GOMOD_SAVED=""; }
trap restore_gomod EXIT

point_at_wick() {
  if [ -n "$WICK_DIR" ]; then
    [ -f "$WICK_DIR/go.mod" ] || die "--wick $WICK_DIR is not a wick checkout"
    save_gomod; run go mod edit -replace "github.com/yogasw/wick=$WICK_DIR"
  elif grep -q '^replace github.com/yogasw/wick' go.mod; then
    die "go.mod has a replace for wick ($(grep '^replace github.com/yogasw/wick' go.mod)). Pass --wick DIR to build with it, or drop it: go mod edit -dropreplace github.com/yogasw/wick"
  elif [ "$UPGRADE" = 1 ]; then
    run wick upgrade -y
  fi
}

not_cloned() { # dry-run of a fresh server: nothing to build until the clone exists
  [ -d "$DIR" ] && return 1
  echo "   (dry-run: $DIR is not cloned yet, nothing more to show)"; }

core() {
  not_cloned && return 0
  cd "$DIR"; pull
  local cur new bin installed=0
  command -v "$APP" >/dev/null 2>&1 && installed=1
  cur="$(awk '/^version:/{print $2; exit}' wick.yml)"; [ -n "$cur" ] || die "no version: in wick.yml"
  if [ "$BUMP" = 1 ] && [ "$installed" = 1 ]; then
    new="$(echo "$cur" | awk -F. -v OFS=. '{$NF=$NF+1; print}')"
    say "version $cur -> $new"; run sedi "s/^version:.*/version: $new/" wick.yml
  fi
  say "wick source: ${WICK_DIR:-release (go.mod)}"; point_at_wick
  run rm -f bin/"$APP"-linux-* bin/"$APP"-darwin-*   # a stale build must never be the one installed
  [ "$TEST" = 1 ] && { say "test"; heavy "$APP-test" env GOFLAGS= go test -p 1 ./...; }
  say "build ($TARGET)"; heavy "$APP-build" wick build --target "$TARGET"
  restore_gomod
  bin="bin/$APP-$OS-$ARCH"
  [ "$DRY" = 1 ] || [ -f "$bin" ] || die "build produced no $bin"
  if [ "$installed" = 0 ]; then
    local deb; deb="$(ls -t bin/"$APP"-*-linux-"$ARCH".deb 2>/dev/null | head -1 || true)"
    if [ "$OS" = linux ] && command -v dpkg >/dev/null && { [ -n "$deb" ] || [ "$DRY" = 1 ]; }; then
      say "first install (.deb)"; run $SUDO dpkg -i "${deb:-bin/$APP-<version>-linux-$ARCH.deb}"
    else
      say "first install (/usr/local/bin/$APP)"; run $SUDO install -m 755 "$bin" "/usr/local/bin/$APP"
    fi
    echo "   installed. Start it with: $APP start   (or: $APP --help)"
    return 0
  fi
  if [ "$RESTART" = 1 ]; then
    local target; target="$(command -v "$APP")"
    say "install with restart (short downtime) -> $target"
    run "$APP" stop || true
    run $SUDO install -m 755 "$DIR/$bin" "$target"
    run "$APP" start
    say "now running"; run "$APP" version
    return 0
  fi
  say "swap + zero-downtime reload"
  # reload checks the candidate (wick module, app, arch, version direction), swaps it
  # in atomically, hands the socket over and rolls back if the successor never serves.
  run "$APP" reload --binary "$DIR/$bin" --sudo -y --wait || \
    die "reload refused or failed — the running binary is unchanged (reason above). If the daemon runs without WICK_GRACEFUL_UPGRADE=1, rerun with --restart (short downtime)."
  say "now running"; run "$APP" version
}

# ---------------------------------------------------------------- plugins
plugin_dirs() { local d
  for d in "$DIR"/plugins/{connector,tool,job,service}/*/; do
    [ -f "$d/VERSION" ] || continue; case "$d" in */_template/) continue ;; esac
    echo "$(basename "$(dirname "$d")")/$(basename "$d")"
  done; }
# KEY KIND -> installed version. A connector and a tool may share a key. No early awk
# exit: under pipefail the writer's SIGPIPE would abort the script.
installed_version() { "$APP" plugin list 2>/dev/null | awk -v k="$1" -v t="$2" '$1==k && $2==t && !f {print $3; f=1}' || true; }
newer() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)" = "$1" ]; }

plugins() {
  not_cloned && return 0
  cd "$DIR"; pull
  command -v "$APP" >/dev/null 2>&1 || [ "$DRY" = 1 ] || die "$APP is not installed — run core first"
  local todo=() kk kind key ver have zip
  for kk in $(plugin_dirs); do
    kind=${kk%/*}; key=${kk#*/}
    if [ ${#KEYS[@]} -gt 0 ]; then [[ " ${KEYS[*]} " == *" $key "* ]] || continue
    elif [ "$ALL_PLUGINS" = 0 ]; then
      ver="$(tr -d '[:space:]' <"plugins/$kind/$key/VERSION")"; have="$(installed_version "$key" "$kind")"
      # never downgrade a plugin installed from another source
      [ -z "$have" ] || newer "$ver" "$have" || continue
    fi
    todo+=("$kk")
  done
  [ ${#todo[@]} -gt 0 ] || { say "plugins: nothing newer than what is installed (--all-plugins or name keys to force)"; return 0; }
  say "plugins: ${todo[*]}"
  [ -n "$WICK_DIR" ] && { save_gomod; run go mod edit -replace "github.com/yogasw/wick=$WICK_DIR"; }
  for kk in "${todo[@]}"; do
    kind=${kk%/*}; key=${kk#*/}
    if [ "$BUMP_PLUGINS" = 1 ] && [ "$DRY" = 0 ]; then
      ver="$(tr -d '[:space:]' <"plugins/$kind/$key/VERSION")"
      echo "$ver" | awk -F. -v OFS=. '{$NF=$NF+1; print}' >"plugins/$kind/$key/VERSION"   # file rewrite, no sed -i
    fi
    ver="$(tr -d '[:space:]' <"plugins/$kind/$key/VERSION")"
    [ "$TEST" = 1 ] && heavy "plugin-test-$key" env GOFLAGS= go test "./plugins/$kind/$key/..."
    # GOWORK=off: a go.work would put plugins/ in workspace mode and break -mod=mod.
    (cd plugins && heavy "plugin-build-$key" env GOWORK=off GOFLAGS= wick plugin build --kind "$kind" "$key" --target "$TARGET")
    zip="plugins/bin/$key-$ver-$OS-$ARCH.zip"
    [ "$DRY" = 1 ] || [ -f "$zip" ] || die "build produced no $zip"
    run "$APP" plugin install "$zip"
    if [ "$DRY" = 0 ]; then # the running app hot-loads it; confirm by version
      for _ in $(seq 1 15); do [ "$(installed_version "$key" "$kind")" = "$ver" ] && break; sleep 2; done
      [ "$(installed_version "$key" "$kind")" = "$ver" ] || die "$key installed but plugin list does not show $ver"
      echo "   ok $kind/$key $ver"; rm -f "$zip"
    fi
  done
  restore_gomod
}

case "$CMD" in
  setup)   resolve_dir; setup ;;
  core)    resolve_dir; core ;;
  plugins) resolve_dir; plugins ;;
  all)     resolve_dir; setup; core; plugins ;;
  status)  resolve_dir; "$APP" version; "$APP" plugin list ;;
  -h|--help|help) usage ;;
  *) usage >&2; exit 2 ;;
esac
