# Docker

Wick ships a multi-arch image at `ghcr.io/yogasw/wick`. Always runs headless (`wick server` under the hood) — there's no desktop session inside a container.

## Quickstart

```bash
docker run -d \
  --name wick \
  -p 9425:9425 \
  -v wick-data:/root/.wick \
  ghcr.io/yogasw/wick:latest
```

Web UI at `http://localhost:9425`. First-boot credentials print to container logs.

::: tip Persist your data
Mount `/root/.wick` to a volume or bind mount. That directory holds SQLite DB, sessions, workspaces, credentials. No mount = everything lost on restart.
:::

## Configure via env

```bash
docker run -d \
  --name wick \
  -p 9425:9425 \
  -v wick-data:/root/.wick \
  -e APP_BASE_URL=https://wick.example.com \
  -e APP_ADMIN_EMAILS=you@example.com \
  ghcr.io/yogasw/wick:latest
```

`APP_BASE_URL` must match the externally-reachable URL — OAuth callbacks and Slack signing rely on it.

## Docker Compose

```yaml
services:
  wick:
    image: ghcr.io/yogasw/wick:latest
    ports:
      - "9425:9425"
    volumes:
      - wick-data:/root/.wick
    environment:
      APP_BASE_URL: https://wick.example.com
      APP_ADMIN_EMAILS: you@example.com
    restart: unless-stopped

volumes:
  wick-data:
```

## Tips & tricks

### Grab the initial creds

```bash
docker logs wick 2>&1 | grep -A1 INITIAL_CREDENTIALS
# or
docker exec wick cat /root/.wick/INITIAL_CREDENTIALS.txt
```

After first login the file is deleted on its own.

### Postgres sidecar

SQLite on a Docker volume works but bind-mount IO can be slow on macOS/Windows hosts. For prod, run Postgres alongside:

```yaml
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_USER: wick
      POSTGRES_PASSWORD: changeme
      POSTGRES_DB: wick
    volumes:
      - pgdata:/var/lib/postgresql/data

  wick:
    image: ghcr.io/yogasw/wick:latest
    depends_on: [db]
    ports:
      - "9425:9425"
    volumes:
      - wick-data:/root/.wick
    environment:
      DATABASE_URL: postgres://wick:changeme@db:5432/wick?sslmode=disable
      APP_BASE_URL: https://wick.example.com
    restart: unless-stopped

volumes:
  wick-data:
  pgdata:
```

### Bake in provider CLIs

The base image ships **without** Claude / Codex / Gemini binaries — wick spawns them but doesn't bundle them. Two ways to make them available:

**Bind-mount from host:**

```yaml
volumes:
  - /usr/local/bin/claude:/usr/local/bin/claude:ro
  - /usr/local/bin/codex:/usr/local/bin/codex:ro
```

**Custom image:**

```dockerfile
FROM ghcr.io/yogasw/wick:latest

# Codex example — adjust for your provider
RUN apt-get update && apt-get install -y nodejs npm \
 && npm install -g @openai/codex \
 && rm -rf /var/lib/apt/lists/*
```

Then point the Provider config (Admin Panel → Providers) at the binary path inside the container — usually `/usr/local/bin/<cli>`.

### Reverse proxy with Traefik

```yaml
services:
  wick:
    image: ghcr.io/yogasw/wick:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.wick.rule=Host(`wick.example.com`)"
      - "traefik.http.routers.wick.tls.certresolver=letsencrypt"
      - "traefik.http.services.wick.loadbalancer.server.port=9425"
    environment:
      APP_BASE_URL: https://wick.example.com
    volumes:
      - wick-data:/root/.wick
```

### Pin a version

Don't run `:latest` in prod — pin to a release tag. List available tags:

```bash
docker pull ghcr.io/yogasw/wick:v0.x.y
```

`:latest` follows the most recent stable release; tags are immutable per release.

## See also

- [Headless Server](/guide/headless) — same `wick server` mode, no container
- [Environment Variables](/reference/env-vars) — full list of `APP_*` knobs
- [Channels](/guide/agents/channels) — Slack Socket Mode works fine inside Docker, no inbound webhook needed
