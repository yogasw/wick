# Web Terminal (WebTTY)

Mounts under `/tools/webtty`. Browser-based terminal proxied ke embedded `gotty` process.

## Use case

**OAuth / interactive login untuk LLM providers** — provider seperti Claude (`claude login`) atau Codex memerlukan interactive browser-based OAuth flow. WebTTY memberi akses terminal langsung dari browser Wick sehingga operator bisa menjalankan login flow tanpa SSH ke server.

Contoh workflow:
1. Buka `/tools/webtty`
2. Klik **Start** — gotty spawn bash/sh session
3. Jalankan `claude login` atau `codex login`
4. Ikuti OAuth flow di terminal
5. Credentials tersimpan di filesystem → Provider Storage Sync backup ke DB otomatis

## Architecture

```
Browser → /tools/webtty/tty/* (WebSocket + HTTP)
             ↓
         tty.Server (gotty proxy)
             ↓
         gotty binary (spawns shell)
```

- Tidak expose port tambahan — semua traffic lewat port Wick utama
- Route `/tty/` di-handle via `HandleRaw` (bypass middleware) untuk WebSocket upgrade
- Static assets (xterm.js, CSS) di-serve dari embedded FS

## Config

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `true` | Toggle terminal on/off tanpa redeploy |

Saat `enabled=false`, halaman tampilkan notice "Terminal Disabled".

## Routes

```
GET  /tools/webtty/          — halaman terminal (iframe + start/stop controls)
GET  /tools/webtty/static/*  — static assets (xterm.js, CSS)
*    /tools/webtty/tty/*     — gotty proxy (HTTP + WebSocket)
```

## Security

- Tool di-tag `System` — hanya admin yang bisa akses
- `Enabled` flag bisa di-toggle dari Settings tanpa restart
- Terminal session scope ke user yang login di browser Wick
