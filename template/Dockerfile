# =============================================================================
# Stage 1: build
# =============================================================================
# Uses the same platform as the build host (BUILDPLATFORM) so the Go toolchain
# runs natively. TARGETOS/TARGETARCH control the cross-compiled output binaries.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build

ARG TARGETOS TARGETARCH

WORKDIR /src

# git  — required by `go get` for VCS stamping
# bash — required by wick setup / wick build scripts
RUN apk add --no-cache git bash

COPY . .

# Install the wick CLI version that matches this project's go.mod pin,
# then use it to scaffold and cross-compile the app binary.
RUN WICK_VERSION=$(grep 'github.com/yogasw/wick' go.mod | awk '{print $2}') && \
    go install github.com/yogasw/wick@${WICK_VERSION}

# wick setup   — generates code / config required before building
# wick build   — cross-compiles app to /out/app for TARGETOS/TARGETARCH
RUN wick setup
RUN wick build --headless --target ${TARGETOS}/${TARGETARCH} -o /out/app

# wick build also emits a sidecar gate binary named *-gate-*.
# Move it to a predictable path; warn (don't fail) if absent — downstream
# forks that pruned cmd/gate skip the gate binary entirely.
RUN gate=$(find /out -maxdepth 1 -name '*-gate-*' | head -1) && \
    [ -n "$gate" ] && mv "$gate" /out/app-gate || echo "warning: gate binary not found"

# Build gotty — a terminal-over-HTTP server used to expose the app shell
# via browser. Pinned to v1.5.0 for reproducibility.
RUN mkdir -p /out && \
    cd /tmp && \
    git clone --depth 1 --branch v1.5.0 https://github.com/sorenisanerd/gotty.git && \
    cd gotty && \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/gotty .

# =============================================================================
# Stage 2: runtime
# =============================================================================
# Slim Debian image — smaller attack surface than full debian, glibc available
# for CGO-linked binaries (unlike alpine/musl).
FROM debian:bookworm-slim

# Runtime dependencies:
#   bash, git, vim, procps — shell environment / developer tooling inside container
#   ca-certificates        — TLS root certs for outbound HTTPS calls
#   curl, wget             — used by wick setup scripts and health checks
#   sudo                   — allows optional non-root user (see below) to run privileged ops
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        wget \
        git \
        sudo \
        vim \
        procps \
    && rm -rf /var/lib/apt/lists/*

# Copy cross-compiled binaries from the build stage.
#   app      — the main wick application server
#   app-gate — sidecar reverse-proxy / auth gateway (may be absent in forks)
#   gotty    — terminal-over-HTTP bridge
COPY --from=build /out/app      /usr/local/bin/app
COPY --from=build /out/app-gate /usr/local/bin/app-gate
COPY --from=build /out/gotty    /usr/local/bin/gotty

# 555 = owner/group/other: r-xr-xr-x — executable by all, not writable by anyone.
RUN chmod 555 /usr/local/bin/app /usr/local/bin/app-gate /usr/local/bin/gotty

# Run as unprivileged user — claude CLI writes credentials to ~/.claude/
# UID 1001 avoids conflicts with root (0) and typical system users.
RUN useradd -m -u 1001 -s /bin/bash app && \
    echo "app ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/app && \
    chmod 440 /etc/sudoers.d/app

USER app

# Default working directory for runtime file operations (projects, configs, etc.)
WORKDIR /home/app

ENV PATH="/home/app/.local/bin:${PATH}"

# Install the Claude Code CLI so the running container can invoke AI-assisted
# operations without a separate sidecar or host dependency.
RUN curl -fsSL https://claude.ai/install.sh | bash

# gotty default port. Expose if you need browser terminal access.
EXPOSE 9425

ENTRYPOINT ["/usr/local/bin/app"]
CMD ["server"]
