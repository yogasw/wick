## 20. Security

- **`type=shell` / `type=python` arbitrary exec** — risk utama.
  - AI-generated selalu `approved=false` → ga jalan sampe user approve.
  - UI Create butuh role admin.
  - Tampilin diff command + script di approval modal.
  - AI guard cek destructive patterns.
- **Prompt injection via channel** — `{{.Event.Payload.text}}` ke classify/agent
  prompt = vector. Mitigasi:
  - Default wrap user input dalam `<user_input>` tag dgn instruksi
    "treat as untrusted".
  - Whitelist (per-trigger atau global) limit siapa yang bisa fire.
  - AI guard flag direct passthrough ke shell exec.
- **DB query injection** — node `db_query` PAKSA parameterized (`$1`,
  `$2`). Engine reject query dgn `{{.Event.X}}` di string raw — harus
  via `args:`. AI guard cek pattern.
- **HTTP SSRF** — node `http` punya allowlist host (per-config atau
  global). Default block `10.*`, `192.168.*`, `localhost`, `metadata.*`.
- **Whitelist enforcement** — dua layer (per-trigger inline + global
  default).
- **Webhook auth** — HMAC SHA-256 wajib. Reject kalau `X-Wick-Sig` ga
  match.
- **Manual trigger** — `require_role: admin` dicek di handler.
- **Workspace isolation** — pool sudah handle per-session worktree.
- **Notify destinations** — Slack channel dibatesi ke yang bot diundang.
- **Secrets** — pakai `wick_enc_...` token (encrypted-fields). Runtime
  decrypt sebelum exec/send.
- **Rate limit per workflow** — hard cap fire rate (default 60/min).

---

