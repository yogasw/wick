#!/usr/bin/env bash
# deploy-from-source.sh — build wick (the wick-agent app) or your own wick app from source
# on Linux or macOS, install it, and hot-reload it with no downtime after that. Installs
# what the build needs (git, Go, the wick CLI) when missing. Windows: deploy-from-source.ps1.
#
#   # wick itself, from branch master (install the first time, update after that)
#   curl -fsSL https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source.sh | bash
#   # … another branch or tag
#   curl -fsSL https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source.sh | bash -s -- --ref develop
#   # your own wick app + its plugins
#   curl -fsSL https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source.sh \
#     | bash -s -- all --git https://github.com/org/myapp.git
#
# See https://yogasw.github.io/wick/guide/deploy-from-source for the full guide.
set -euo pipefail

usage() { cat <<'EOF'
Usage: deploy-from-source.sh [command] [flags] [plugin-key...]

Commands
  wick      (default) build wick itself from source at --ref: the wick CLI and the
            wick-agent app (scaffolded once in ~/wick-agent, built against the source),
            installed the first time, zero-downtime reload after that
  setup     install git, Go (the version the app's go.mod asks for) and the wick CLI
  core      build the app binary and swap it in: zero-downtime reload when the app is
            running, a fresh .deb install when it is not installed yet
  plugins   build + install plugins under plugins/{connector,tool,job,service}/<key>
            (default: only those whose VERSION is newer than the installed one, or new)
  all       setup, core, then plugins
  status    app version and installed plugins

Flags
  --ref REF        wick: branch or tag to build (default: master)
  --src DIR        wick: source checkout (default: ~/wick-src)
  --dir DIR        app checkout (default: current dir if it has wick.yml, else ~/<repo name>;
                   wick: ~/wick-agent)
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
  --restart        update with stop / swap / start even when a zero-downtime reload is
                   possible (picked automatically when the daemon cannot reload)
  --no-install     build only: leave the result in bin/ and install nothing
  --force          wick: rebuild even when the installed build is already from the newest commit
  --dry-run        print what would run, change nothing
EOF
}

CMD=wick; case "${1:-}" in ""|-*) ;; *) CMD=$1; shift ;; esac
DIR=""; GIT_URL=""; BRANCH=""; APP=""; WICK_DIR=""; REF=master; SRC="$HOME/wick-src"; FORCE=""; BUILT_SHA=""
UPGRADE=1; PULL=1; BUMP=1; TEST=1; ALL_PLUGINS=0; BUMP_PLUGINS=0; DRY=0; RESTART=0; NOINSTALL=0; KEYS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --dir) DIR="$2"; shift 2 ;;
    --ref) REF="$2"; shift 2 ;;
    --src) SRC="$2"; shift 2 ;;
    --git) GIT_URL="$2"; shift 2 ;;
    --branch) BRANCH="$2"; shift 2 ;;
    --app) APP="$2"; shift 2 ;;
    --wick) WICK_DIR="$(cd "$2" && pwd)"; shift 2 ;;
    --restart) RESTART=1; shift ;;
    --no-install) NOINSTALL=1; shift ;;
    --force) FORCE=1; shift ;;
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
  if command -v buildq >/dev/null 2>&1; then run buildq -n "$label" -m 2100M -- "$@"; else run nice -n 10 "$@"; fi; }

case "$(uname -m)" in x86_64|amd64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) die "unsupported arch $(uname -m)" ;; esac
case "$(uname -s)" in Linux) OS=linux ;; Darwin) OS=darwin ;; *) die "unsupported OS $(uname -s) — on Windows use deploy-from-source.ps1" ;; esac
TARGET="$OS/$ARCH"
sedi() { local f=${*: -1}; sed -i.bak "$@" && rm -f "$f.bak"; }   # sed -i on GNU and BSD (macOS)
export PATH="/usr/local/go/bin:/usr/local/node/bin:$HOME/go/bin:$PATH"

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

go_wanted() { # GOMOD -> the Go version it asks for (toolchain wins), else the latest
  local v="" m=$1
  if [ -f "$m" ]; then
    v="$(awk '/^toolchain go/{sub("go","",$2); print $2; exit}' "$m")"
    [ -n "$v" ] || v="$(awk '/^go [0-9]/{print $2; exit}' "$m")"
  fi
  [ -n "$v" ] || v="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1 | sed 's/^go//')"
  echo "$v"
}

ver_ge() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -1)" = "$2" ]; }   # ver_ge A B: A >= B

setup_go() { # GOMOD
  local want have=""; want="$(go_wanted "$1")"
  command -v go >/dev/null 2>&1 && have="$(GOTOOLCHAIN=local go env GOVERSION | sed 's/^go//')"
  # An existing Go is never replaced: other projects on the machine use it. Go 1.21+
  # fetches the toolchain a go.mod asks for by itself (GOTOOLCHAIN=auto), so an older
  # one only needs that switch left on. Install only when there is no Go, or one too
  # old to auto-switch.
  if [ -n "$have" ] && ! ver_ge "$have" "$want" && ver_ge "$have" 1.21; then
    echo "   Go $have, go.mod asks $want: Go downloads that toolchain itself (GOTOOLCHAIN=auto)"
    [ "$(go env GOTOOLCHAIN)" != local ] || export GOTOOLCHAIN=auto
  elif [ -z "$have" ] || ! ver_ge "$have" "$want"; then
    [ -z "$have" ] || [ -n "${FORCE_GO:-}" ] || die "Go $have is too old to auto-switch (< 1.21) and is not replaced on its own. Upgrade it, or rerun with FORCE_GO=1 to replace /usr/local/go with Go $want."
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
  # go install puts tools (wick, templ from `wick setup`) in GOPATH/bin: it must be on PATH
  # for the rest of this run, or `wick build` fails on "templ: executable file not found".
  command -v go >/dev/null 2>&1 && export PATH="${GOBIN:+$GOBIN:}$(go env GOPATH)/bin:$PATH"
}

setup() {
  need_pkgs git curl tar
  setup_go "$DIR/go.mod"
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
  install_build "$installed"
}

# start_app: as a systemd user service when the machine has one — the packaged unit runs
# with WICK_GRACEFUL_UPGRADE=1, which is what makes every later update a zero-downtime
# reload, and it survives a reboot — else as a plain daemon (`<app> start`).
start_app() {
  # a unit that already exists (system-wide, or the user one from an earlier run) is reused
  if [ "$OS" = linux ] && systemctl cat "$APP.service" >/dev/null 2>&1; then
    say "restart system service $APP"; run $SUDO systemctl restart "$APP.service"
  elif [ "$OS" = linux ] && systemctl --user cat "$APP.service" >/dev/null 2>&1; then
    say "restart user service $APP"; run systemctl --user restart "$APP.service"
  elif [ "$OS" = linux ] && systemctl --user show-environment >/dev/null 2>&1; then
    say "start $APP as a systemd user service (boot start + zero-downtime updates)"
    # DISPLAY/WAYLAND_DISPLAY unset: with them `service install` picks the desktop autostart
    # entry (a tray app at login) instead of the systemd user unit a server needs.
    run env -u DISPLAY -u WAYLAND_DISPLAY "$APP" service install
    run systemctl --user restart "$APP.service"
  else
    say "start $APP"
    run "$APP" start || echo "   start failed — run '$APP start' by hand (or '$APP server' in the foreground)"
  fi
}

# graceful: is the running daemon able to take a zero-downtime reload? Only one started with
# WICK_GRACEFUL_UPGRADE=1 (the systemd unit) acts on it; a plain `<app> start` ignores it.
# Prints running/graceful/stopped.
daemon_mode() {
  local pid env; pid="$(pgrep -x "$APP" 2>/dev/null | head -1 || true)"
  [ -n "$pid" ] || { echo stopped; return; }
  env="$(tr '\0' '\n' <"/proc/$pid/environ" 2>/dev/null || $SUDO tr '\0' '\n' <"/proc/$pid/environ" 2>/dev/null || true)"
  if printf '%s\n' "$env" | grep -qx 'WICK_GRACEFUL_UPGRADE=1'; then echo graceful; else echo running; fi
}

# done_note: what to do once wick-agent is up — the questions a first run leaves.
done_note() {
  local ip; ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  cat <<EOF

== done — $APP is installed${1:+ ($1)}
   open      http://${ip:-<this-host>}:9425   (first admin login: ~/.$APP/INITIAL_CREDENTIALS.txt)
   status    $APP status            logs: $APP status shows the daemon log tail
   stop/run  systemctl --user stop|start $APP   (or $APP stop|start without the service)
   update    run this same command again — it skips the build when $REF has no new commit
   note      'wick' is the dev CLI (build/init); the server is '$APP'
EOF
}

# mark_built records the source commit the installed binary was built from (wick mode),
# so a rerun on the same commit skips the build. Written once the binary is in place.
mark_built() {
  [ -n "$BUILT_SHA" ] && [ "$DRY" = 0 ] || return 0
  mkdir -p "$HOME/.$APP" && echo "$BUILT_SHA" >"$HOME/.$APP/built-commit" || true
}

# install_build INSTALLED — put the build (cwd = the app) in place: a first install + start,
# else the zero-downtime reload when the daemon supports it, else stop / swap / start.
install_build() {
  # the build is named after wick.yml's name:, which --app (the installed command) may differ from
  local installed=$1 name bin mode target
  name="$(awk '/^name:/{print $2; exit}' wick.yml 2>/dev/null)"; bin="bin/${name:-$APP}-$OS-$ARCH"
  [ "$DRY" = 1 ] || [ -f "$bin" ] || die "build produced no $bin"
  if [ "$NOINSTALL" = 1 ]; then say "built $PWD/$bin (--no-install: nothing installed)"; return 0; fi
  if [ "$installed" = 0 ]; then
    local deb; deb="$(ls -t bin/"${name:-$APP}"-*-linux-"$ARCH".deb 2>/dev/null | head -1 || true)"
    if [ "$OS" = linux ] && command -v dpkg >/dev/null && { [ -n "$deb" ] || [ "$DRY" = 1 ]; }; then
      say "first install (.deb)"; run $SUDO dpkg -i "${deb:-bin/$APP-<version>-linux-$ARCH.deb}"
    else
      say "first install (/usr/local/bin/$APP)"; run $SUDO install -m 755 "$bin" "/usr/local/bin/$APP"
    fi
    mark_built
    start_app
    echo "   web UI: http://<this-host>:9425 · first-boot admin login: ~/.$APP/INITIAL_CREDENTIALS.txt"
    echo "   update later: run this same command again   (the 'wick' command is the dev CLI, not the server)"
    return 0
  fi
  mode="$(daemon_mode)"; [ "$RESTART" = 1 ] && [ "$mode" != stopped ] && mode=running
  target="$(command -v "$APP")"
  case "$mode" in
    graceful)
      say "swap + zero-downtime reload"
      # reload checks the candidate (wick module, app, arch, version direction), swaps it
      # in atomically, hands the socket over and rolls back if the successor never serves.
      # --sudo only when not root: a root shell on a fresh server often has no sudo at all.
      run "$APP" reload --binary "$PWD/$bin" ${SUDO:+--sudo} -y --wait || \
        die "reload refused or failed — the running binary is unchanged (reason above)."
      mark_built
      ;;
    running)
      say "update with stop / swap / start (short downtime: $APP was started without graceful upgrade)"
      run "$APP" stop || true
      run $SUDO install -m 755 "$PWD/$bin" "$target"; mark_built
      start_app
      ;;
    stopped)
      say "update $target ($APP is not running)"
      run $SUDO install -m 755 "$PWD/$bin" "$target"; mark_built
      start_app
      ;;
  esac
  say "now running"; run "$APP" version
}

# ---------------------------------------------------------------- wick itself
setup_node() { # Node 20+ for the FE build (the release CI uses 20)
  local have=""; command -v node >/dev/null 2>&1 && have="$(node -v | sed 's/^v//')"
  if [ -n "$have" ] && ver_ge "$have" 20.0.0; then echo "   Node $have ok"; return 0; fi
  local na=x64; [ "$ARCH" = arm64 ] && na=arm64
  local tgz; tgz="$(curl -fsSL https://nodejs.org/dist/latest-v20.x/SHASUMS256.txt | grep -o "node-v[0-9.]*-$OS-$na\.tar\.gz" | head -1)"
  [ -n "$tgz" ] || die "could not resolve a Node 20 download for $OS-$na"
  say "install Node ${tgz%%-$OS*} (have: ${have:-none}) -> /usr/local/node"
  local tmp; tmp="$(mktemp -d)"
  run curl -fsSL -o "$tmp/$tgz" "https://nodejs.org/dist/latest-v20.x/$tgz"
  run $SUDO rm -rf /usr/local/node
  run $SUDO mkdir -p /usr/local/node
  run $SUDO tar -C /usr/local/node --strip-components=1 -xzf "$tmp/$tgz"
  rm -rf "$tmp"
  export PATH="/usr/local/node/bin:$PATH"
  grep -qs '/usr/local/node/bin' "$HOME/.profile" || [ "$DRY" = 1 ] || \
    echo 'export PATH="/usr/local/node/bin:$PATH"' >>"$HOME/.profile"
}

# prep_wick_source generates what master/develop do not carry in git and a release tag
# does (release.yml bundles them into the tag commit): templ's *_templ.go, and every
# Vite SPA under fe/ (embedded from internal/**/dist). Without it the build fails on
# "no required module provides package …/view".
prep_wick_source() {
  (
    cd "$SRC" 2>/dev/null || exit 0
    local tv; tv="$(GOWORK=off GOFLAGS= go list -m -f '{{.Version}}' github.com/a-h/templ 2>/dev/null || echo latest)"
    say "templ $tv: generate"
    run env GOWORK=off GOFLAGS= go install "github.com/a-h/templ/cmd/templ@$tv"
    heavy wick-templ templ generate ./...
    say "FE build (fe/: npm ci + npm run build)"
    (cd fe && heavy wick-fe-deps npm ci --no-audit --no-fund && heavy wick-fe-build env NODE_OPTIONS=--max-old-space-size=2048 npm run build)
  )
}

# wick-agent is what the release ships: `wick init wick-agent` from the wick CLI, built
# with `wick build`. From source the same app is scaffolded once and built against the
# checkout at --ref, so a branch with unreleased commits installs like a release.
wick_self() {
  DIR="${DIR:-$HOME/wick-agent}"; APP="${APP:-$(basename "$DIR")}"
  need_pkgs git curl tar
  if [ -d "$SRC/.git" ]; then
    say "wick source $SRC -> $REF"
    run git -C "$SRC" fetch --quiet origin "$REF"
    run git -C "$SRC" checkout --quiet --force --detach FETCH_HEAD   # a build dir: local edits are not kept
  else
    say "clone wick ($REF) -> $SRC"
    run git clone --quiet --branch "$REF" https://github.com/yogasw/wick.git "$SRC"
  fi
  # Same commit as the installed build: nothing to rebuild, only make sure it runs.
  local sha="" stamp="$HOME/.$APP/built-commit"   # next to the app's own data (~/.wick-agent)
  [ -d "$SRC/.git" ] && sha="$(git -C "$SRC" rev-parse HEAD 2>/dev/null || true)"
  if [ -n "$sha" ] && [ -z "$FORCE" ] && command -v "$APP" >/dev/null 2>&1 && \
     [ "$(cat "$stamp" 2>/dev/null)" = "$sha" ]; then
    say "$APP is already built from ${sha:0:12} ($REF) — nothing to rebuild (--force to rebuild anyway)"
    case "$(daemon_mode)" in
      stopped) start_app ;;
      running) echo "   running without graceful upgrade: the next update restarts it once and moves it onto the service" ;;
      graceful) echo "   running (zero-downtime updates on)" ;;
    esac
    done_note "${sha:0:12}"
    return 0
  fi
  BUILT_SHA="$sha"
  setup_go "$SRC/go.mod"
  setup_node
  prep_wick_source
  say "wick CLI from source"
  (cd "$SRC" 2>/dev/null || cd "$HOME"; heavy wick-cli env GOWORK=off GOFLAGS= go install .)
  local installed=0 base ver
  command -v "$APP" >/dev/null 2>&1 && installed=1
  if [ ! -f "$DIR/wick.yml" ]; then
    say "scaffold $DIR"
    run mkdir -p "$(dirname "$DIR")"
    if [ "$DRY" = 1 ]; then echo "   \$ (cd $(dirname "$DIR") && wick init $(basename "$DIR") --skip-setup)"
    else (cd "$(dirname "$DIR")" && run wick init "$(basename "$DIR")" --skip-setup); fi
  fi
  [ "$DRY" = 1 ] && [ ! -d "$DIR" ] && { echo "   (dry-run: $DIR is not scaffolded yet, nothing more to show)"; return 0; }
  cd "$DIR"
  run go mod edit -replace "github.com/yogasw/wick=$SRC"
  say "setup (tailwind, templ, go mod tidy)"; heavy "$APP-setup" env GOFLAGS= wick setup
  # Version: the source's VERSION, next patch, plus the build time as a 4th number
  # (1.17.2.202610191010). reload compares versions numerically and ignores a -suffix, so
  # this is what makes every source build newer than the release before it and than the
  # previous source build.
  base="$(tr -d '[:space:]' <"$SRC/VERSION" 2>/dev/null || echo 0.0.0)"
  ver="$(echo "${base#v}" | awk -F. -v OFS=. '{$3=$3+1; print $1,$2,$3}').$(date +%Y%m%d%H%M)"
  say "version $ver"; run sedi "s/^version:.*/version: $ver/" wick.yml
  run rm -f bin/"$APP"-linux-* bin/"$APP"-darwin-*
  say "build ($TARGET)"; heavy "$APP-build" env GOFLAGS= wick build --target "$TARGET"
  install_build "$installed"
  [ "$NOINSTALL" = 1 ] || done_note "${BUILT_SHA:0:12}"
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
  wick)    wick_self ;;
  setup)   resolve_dir; setup ;;
  core)    resolve_dir; core ;;
  plugins) resolve_dir; plugins ;;
  all)     resolve_dir; setup; core; plugins ;;
  status)  resolve_dir; "$APP" version; "$APP" plugin list ;;
  -h|--help|help) usage ;;
  *) usage >&2; exit 2 ;;
esac
