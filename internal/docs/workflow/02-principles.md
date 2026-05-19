## 2. Prinsip

0. **AI-first design — mandatory.** Workflow harus bisa dibuat efektif
   oleh AI (Claude Code, Cursor, Claude Desktop, ChatGPT custom GPT,
   Gemini Gem) dengan **cuma prompt user natural language**, tanpa user
   tulis YAML manual. Pola sama dengan tools/jobs/connectors yang sudah
   ada di wick — AI = primary author, UI = secondary review/tweak. Test
   AI-first dgn checklist berikut sebelum impl ship:

   - **Schema fully introspectable via MCP.** Setiap node type, trigger
     type, channel, connector op, skill, dataset, dan provider tersedia
     via MCP introspection op (`workflow_node_types`, `workflow_trigger_types`,
     `workflow_channels`, `workflow_connectors`, `workflow_skills`,
     `workflow_providers`). Tiap return JSON schema + description +
     example.
   - **Description load-bearing.** Tiap node type, op, trigger punya
     `description` yang AI baca verbatim (same discipline sebagai connector
     `Operation.Description` di [docs/guide/connector-module.md](../../docs/guide/connector-module.md)).
     Action verb + what it does + when to use. ✅ "Send Slack reply to
     {thread}. Returns posted message timestamp." ❌ "send slack".
   - **Naming consistency.** Predictable conventions — `type: channel`
     simetric trigger+action, `<channel>.<op>` skill-promoted naming,
     `{{.Event.X}}` / `{{.Node.X}}` / `{{.Env.X}}` template refs. AI
     ga perlu trial-error untuk guess naming.
   - **Validate + simulate + test sebelum deploy.** AI bisa iterate di
     `workflow_validate` (parse + cycle + schema), `workflow_simulate`
     (event sintetis, no side effect), `workflow_test` (fixtures). Error
     messages structured + actionable (path field, expected vs got).
   - **Scaffold templates.** `workflow_create(id, template)` punya 4+
     starter (empty, support-triage, incident-response, daily-digest)
     yang AI extend, bukan generate from-scratch.
   - **Canvas ops as alternative to file edit.** AI di remote env (Claude
     Desktop, ChatGPT) ga punya file tool — pakai `workflow_add_node`,
     `workflow_connect`, etc. Same outcome (file di folder), beda channel.
   - **Composition over invention.** AI compose dari building blocks yang
     sudah ada (connector ops, channel actions, skills, dataset). Ga
     perlu mikir "gimana cara call GitHub API" — connector existing handle.
     Adding new integration = bikin connector module (existing pattern,
     well-documented).

   By design wick adalah AI-buildable workflow engine, bukan
   workflow-engine-with-AI-as-afterthought. Setiap design decision di doc
   ini di-evaluate melalui filter "apakah AI bisa compose ini lewat MCP +
   prompt?". Kalau jawaban "perlu human read docs first" → redesign.

1. **File-based, UI = primary editor.** Workflow = folder
   `<BaseDir>/workflows/<id>/` dengan `workflow.yaml` (graph + triggers)
   + folder `nodes/` (per-node script/prompt). File adalah storage,
   canvas adalah surface utama. Hand-edit YAML tetep didukung (gitops,
   power-user). Atomic write via `tmp+rename`.
2. **Domain di [internal/agents/workflow/](../agents/workflow/).** Sejajar
   dengan `preset/`, `workspace/`, `gate/`, `session/`. Service-nya satu,
   dipake tiga caller: UI handler, MCP connector, runtime engine.
3. **DAG-capable engine, tree-shape default.** Engine support multi-parent
   + merge (wait-for-all) + parallel fan-out. Tapi workflow yang dibuat
   user/AI biasanya tree (single-parent, sequential branches). Engine
   ga enforce shape — admin bebas.
4. **Node-based, polymorphic via `type:` field.** Tipe node:
   `classify, agent, channel, connector, shell, python, http, db_query, transform,
   branch, parallel, merge, end`. Dispatcher di runtime per-type.
5. **Output reference: `{{.Node.<id>.<field>}}`.** Tiap node simpan
   output ke run context, downstream node baca via template.
6. **Trigger polymorphic — cron cuma salah satu.** Workflow punya
   `triggers: []Trigger` list. Tipe: `cron`, `channel`,
   `webhook`, `manual`, `schedule_at`. Satu workflow boleh multi-trigger.
7. **Per-workflow FIFO queue.** Concurrent fire di-queue per workflow.
   Worker pool ngedrain serial — gak ada race condition antar run dalam
   1 workflow. Cross-workflow tetep paralel.
8. **State file-based, resume-able.** Tiap run = folder
   `runs/<run-id>/` dengan `state.json` + `events.jsonl`. Crash di
   tengah → resume dari node terakhir sukses. Mirip session di
   [internal/agents/session/](../agents/session/) — file-based, ga
   butuh DB.
9. **Test mode wajib.** Tiap node bisa punya fixture di `__tests__/`.
   "Test Workflow" button jalankan dgn fixture, output kelihatan
   color-coded di canvas. AI/admin iterasi tanpa fire trigger asli.
10. **AI bisa edit canvas via MCP.** Canvas ops (`add_node`, `connect`,
    `update_node`, `delete_node`) di-expose ke MCP. AI bisa "buatkan
    workflow: trigger Slack `!support`, klasifikasi, kalau bug bikin
    Linear ticket, kalau pertanyaan jawab dgn skill docs-search."
11. **AI guard sebelum publish.** Saat user/MCP commit workflow baru
    atau enable → optional AI reviewer baca semua node + edges + script
    + prompt, banding dgn rule. Block kalau melanggar, audit kalau
    override.
12. **Skill ≠ workflow.** Skill = local Claude Code skill bundle (atomic
    AI capability, provider-specific). Workflow = multi-node graph.
    Skill diakses cuma di dalam `type: agent` node lewat `skills: []`
    field — BUKAN standalone node type. Channel actions + connector ops
    punya node type sendiri (`type: channel`, `type: connector`).

---

