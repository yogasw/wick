---
layout: home

hero:
  name: "Wick"
  text: "Run Claude / Codex / Gemini as a Slack + Telegram + Web agent host. One binary."
  tagline: Download <strong>Wick Agent</strong>, point it at your AI CLI of choice, and your team gets a multi-channel agent in minutes — no Go, no scaffolding, no copy-pasting. Or build internal tools in Go with the <strong>Wick Framework</strong>, where Claude scaffolds tools, jobs, and connectors as real files in your repo.
  image:
    src: /logo.svg
    alt: Wick
  actions:
    - theme: brand
      text: Wick Agent (no Go needed)
      link: /guide/agents-only
    - theme: alt
      text: Wick Framework (build in Go)
      link: /guide/getting-started

features:
  - icon: ⚡
    title: Two Ways to Use Wick
    details: |
      <strong>Agent host only</strong> — download the binary (or pull the Docker image), point it at your Claude / Codex / Gemini install, and get a Slack + Telegram + web AI agent in minutes. No Go, no framework, no scaffolding needed.
      <br><br>
      <strong>Framework</strong> — run <code>wick init</code>, open Claude Code, and prompt your way to internal tools, background jobs, and LLM-facing connectors. All real Go files in your repo — <code>git diff</code> to review, <code>git revert</code> to undo.
  - icon: 💬
    title: AI Agents in Slack, Telegram, and the Web
    details: |
      Spawn Claude / Codex / Gemini as long-lived subprocesses — same agent reachable from Slack threads, Telegram chats, and the web UI at the same time.
      <br><br>
      Multi-session pool with idle-kill + <code>--resume</code> revive · multi-instance providers (two PATs, side-by-side) · projects on disk · <a href="/wick/guide/command-gate">command gate</a> with 4-mode interactive approval · AskUser MCP tool · everything persisted under <code>~/.<app>/agents/</code>.
  - icon: 🤖
    title: AI Is the Primary User
    details: Wick is designed for AI agents, not humans. Every convention, file name, and pattern is optimized so Claude knows exactly what to create — no exploration, no guessing.
  - icon: 🗂️
    title: Git Is the Control Plane
    details: No drag-and-drop UI to version. Every tool and job AI creates is real code in real files. `git diff` to review, `git revert` to undo. You own everything.
  - icon: 🧰
    title: Tools, Jobs, & Connectors
    details: Say "add a Slack notifier job" or "add a GitHub connector for our LLM agent". Claude creates the file, registers it, wires the config — for humans, schedulers, and LLMs alike.
  - icon: 🔌
    title: LLM-Ready via MCP
    details: Expose any connector to Claude, Cursor, and other MCP clients. Built-in OAuth 2.1 + Personal Access Tokens, per-call audit log, no protocol code on your side.
  - icon: 🛡️
    title: Command Gate
    details: |
      Every Bash command an agent runs goes through a sidecar binary you ship with the installer. Whitelist via glob, escalate to interactive approval (Approve once / This session / Always / Block), audited per-stage to JSONL.
      <br><br>
      No env vars. Sibling-of-exe → embedded fallback → PATH. Just works.
  - icon: 👀
    title: See Everything That Was Built
    details: Git history IS your tool inventory. Who built what, when, and why — no separate dashboard or admin panel to maintain.
  - icon: 🔐
    title: Secure by Default
    details: First boot generates a random admin passphrase and forces a setup flow before anything else runs. SSO, per-tool visibility, tag-based access — all editable from /admin without a redeploy.
  - icon: ⚙️
    title: Live Config, No Redeploy
    details: Declare a typed Config struct. Fields become admin-editable rows. Secrets, URLs, toggles — updated live without touching code.
---

<div class="agents-spotlight">

## Wick Agent — the primary use case

Want Claude / Codex / Gemini as a Slack bot, Telegram bot, or web assistant? Wick Agent is one self-contained binary. Install + run = working agent.

```bash
# Linux / macOS / Termux — auto-detects OS + arch
curl -fsSL https://yogasw.github.io/wick/install.sh | sh
wick-agent start              # daemon mode — manages its own PID + log
```

```powershell
# Windows — system tray
iwr -useb https://yogasw.github.io/wick/install.ps1 | iex
wick-agent.exe
```

```bash
# Docker
docker run -d -p 9425:9425 -v wick-data:/root/.wick ghcr.io/yogasw/wick:latest
```

Web UI at `http://localhost:9425`. Initial credentials in the daemon log / container stdout.

→ [Wick Agent quickstart](/guide/agents-only) · [Channels](/guide/agents/channels) · [Providers](/guide/agents/providers) · [Command Gate](/guide/command-gate)

---

## Wick Framework — build internal tools in Go

Need humans-facing tool pages, scheduled jobs, or LLM-facing connectors that live in your codebase? The framework lets Claude scaffold them as real Go files.

```bash
go install github.com/yogasw/wick@v0.17.0
wick init my-app
cd my-app && wick dev
```

Open the project in Claude Code. Prompt what you need:

```
add a tool called "base64" that encodes and decodes text
add a background job that syncs data from our API every 30 minutes
add a connector for GitHub with list_repos, create_issue operations
```

Claude writes real Go files. `git diff` to review, `git revert` to undo. You own everything.

→ [Framework quickstart](/guide/ai-quickstart) · [Tool Module](/guide/tool-module) · [Job Module](/guide/job-module) · [Connector Module](/guide/connector-module)

## Why teams pick wick for AI agents

Most "AI agent" platforms lock you into their runtime, expose chat-only, and hide the moving parts. Wick does the opposite:

| You bring | Wick gives you |
|---|---|
| Your **Claude / Codex / Gemini** install (with your MCP servers, skills, memory) | A pool that spawns them as subprocesses, idle-kills, resumes by `cli_session_id` |
| Your **Slack workspace, Telegram bot, or just a browser** | Three transports → one session per thread / chat / conversation, all live at once |
| Your **PAT** (or two — `claude/work` + `claude/personal`) | Multi-instance provider config, per-instance env / args, status-cached `--version` probes |
| Your **folder of repos** (or any path on disk) | Projects — managed or custom path, multi-session sharing, no git worktree |
| Your **trust threshold** | Command Gate sidecar: whitelist + 4-mode interactive approval, fail-safe block on infra failure |

Read [AI Agents](/guide/agents) for the headline tour, or jump to the deep-dives:

- [Agent Host Only](/guide/agents-only) — download binary / Docker, no Go needed
- [Projects](/guide/agents/projects) — folders on disk, managed vs custom path, the built-in `default`
- [Providers](/guide/agents/providers) — multi-instance config, binary resolution chain, status cache
- [Channels](/guide/agents/channels) — Slack (Socket + HTTP), Telegram (long-poll), Web (SSE), AskUser MCP
- [Pool & Sessions](/guide/agents/pool) — slot allocation, message buffer, resume flow
- [Command Gate](/guide/command-gate) — `<app>-gate` sidecar, shared spec/socket/audit, daily tail log

</div>
