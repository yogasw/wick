---
layout: home

hero:
  name: "Wick"
  text: "Build internal tools and AI agents in Go."
  tagline: Scaffold a tool, job, or connector with one prompt — Claude writes real Go files in your repo. Ship the same binary as a Slack / Telegram / web AI agent with command-approval, multi-session pool, and zero copy-pasting.
  image:
    src: /logo.svg
    alt: Wick
  actions:
    - theme: brand
      text: Start with AI
      link: /guide/ai-quickstart
    - theme: alt
      text: AI Agents Guide
      link: /guide/agents
    - theme: alt
      text: Manual Setup
      link: /guide/getting-started

features:
  - icon: 💬
    title: AI Agents in Slack, Telegram, and the Web
    details: |
      Spawn Claude / Codex / Gemini as long-lived subprocesses — same agent reachable from Slack threads, Telegram chats, and the web UI at the same time.
      <br><br>
      Multi-session pool with idle-kill + <code>--resume</code> revive · multi-instance providers (two PATs, side-by-side) · workspaces on disk · <a href="/wick/guide/command-gate">command gate</a> with 4-mode interactive approval · AskUser MCP tool · everything persisted under <code>~/.&lt;app&gt;/agents/</code>.
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

## Why teams pick wick for AI agents

Most "AI agent" platforms lock you into their runtime, expose chat-only, and hide the moving parts. Wick does the opposite:

| You bring | Wick gives you |
|---|---|
| Your **Claude / Codex / Gemini** install (with your MCP servers, skills, memory) | A pool that spawns them as subprocesses, idle-kills, resumes by `cli_session_id` |
| Your **Slack workspace, Telegram bot, or just a browser** | Three transports → one session per thread / chat / conversation, all live at once |
| Your **PAT** (or two — `claude/work` + `claude/personal`) | Multi-instance provider config, per-instance env / args, status-cached `--version` probes |
| Your **folder of repos** (or any path on disk) | Workspaces — managed or custom path, multi-session sharing, no git worktree |
| Your **trust threshold** | Command Gate sidecar: whitelist + 4-mode interactive approval, fail-safe block on infra failure |

Read [AI Agents](/guide/agents) for the headline tour, or jump to the deep-dives:

- [Workspaces](/guide/agents/workspaces) — folders on disk, managed vs custom path, the built-in `default`
- [Providers](/guide/agents/providers) — multi-instance config, binary resolution chain, status cache
- [Channels](/guide/agents/channels) — Slack (Socket + HTTP), Telegram (long-poll), Web (SSE), AskUser MCP
- [Pool & Sessions](/guide/agents/pool) — slot allocation, message buffer, resume flow
- [Command Gate](/guide/command-gate) — `<app>-gate` sidecar, shared spec/socket/audit, daily tail log

</div>
