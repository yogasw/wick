## 1. Latar belakang

Wick punya tiga kelas modul:

- **Tools** — UI manual buat manusia.
- **Connectors** — kapabilitas terstruktur buat LLM lewat MCP.
- **Jobs** — RunFunc statis yang dijalanin scheduler cron tiap menit.

Yang kurang: admin (dan AI) bisa **bikin tugas otomatis multi-step tanpa
recompile**. Step bisa: tanya AI, klasifikasi, query DB, call HTTP,
exec shell, panggil skill, branching kondisional. Dipicu cron, pesan
Slack, webhook, atau klik manual. Plus AI lewat MCP bisa nge-design
workflow sendiri di canvas: drag node, sambungin edge, test, deploy.

Hari ini skenario itu mengharuskan: tulis Go module → register → build →
deploy. **Workflow** = entry baru sebagai folder di
`<BaseDir>/workflows/`, ngikutin pola preset/workspace yang sudah ada.

Konsepnya mirip n8n/Zapier (visual node-based automation) tapi
**AI-native**: node `classify` pakai LLM buat decision, AI bisa
edit canvas via MCP, AI guard review workflow sebelum publish, dan
runtime nyambungin natural ke channel registry + connector registry + agent pool.

### Differentiator vs n8n

| Aspect | n8n | Wick Workflow |
|---|---|---|
| Decision logic | If/Switch dgn expression | `branch` (if/switch) + `classify` (AI natural lang) — dua-duanya tersedia |
| AI integration | API SDK per node (OpenAI/Anthropic) — 1 node = 1 API call | CLI subprocess (`claude`/`codex`/`gemini`) — AI = agent dgn tool ecosystem built-in (Read/Edit/Bash/MCP) |
| Output reliability | `tool_use` schema enforcement di API level | Prompt-based JSON + parser + 5-layer fallback (lihat §5.1) |
| AI nodes | Add-on, generic LLM call | First-class, agent pool + session reuse, share state antar node |
| Editor | Wajib UI | UI **atau** YAML **atau** AI-via-MCP — semua valid |
| Storage | DB | File-based, gitops-friendly |
| External integration | Generic webhook + custom code per node | Reuse connector module (existing wick infra). Channel module = bidirectional via `type: channel` symmetric trigger+action. Skill = local agent capability inside `type: agent` |
| Self-built | Hosted/self-host k8s | Embedded in wick binary |
| Latency per AI node | ~200-1000ms (API direct) | ~500-2000ms (proc spawn overhead) — accepted trade-off |

Bukan kompetitor n8n. n8n = generic workflow engine dgn API-SDK approach;
wick = AI-agent orchestration dgn CLI subprocess approach. Overlap area =
"automation", tapi pendekatan AI fundamentally beda:

- **n8n** call OpenAI API per node, each independent. Cocok buat "LLM
  as one of many service integrations".
- **Wick** spawn agent CLI yang punya tool ecosystem inheriting dari
  agent session. Cocok buat "AI orchestration dgn skill + file
  manipulation + MCP tools".

Workflow ga ngejar fitur n8n generic (CRM connector, Sheets transform,
dst). Ngejar "LLM + skill + channel" pipeline yang n8n kurang ergonomis
karena AI di n8n cuma node biasa, sementara di wick AI adalah
orchestrator dgn tools.

---

