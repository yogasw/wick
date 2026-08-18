---
name: wick-agents
description: Use when the user asks how wick agents work — sessions, projects, presets, providers, channels (Slack/Telegram/web), the pool and its queue, scheduled messages, or where agent state lives on disk. Covers the mental model behind sessions and projects, how to point an agent at a folder, and how to reach the same agent from several channels.
---

# Using wick agents

An **agent** in wick is a CLI coding assistant (Claude Code, Codex, Gemini) or the in-process `wick` provider, run as a subprocess against a folder, reachable from several channels at once. Everything it does happens inside a **session**.

## The mental model

Four things, in order of how often you touch them:

| | What it is |
|---|---|
| **Session** | One conversation. Auto-created on the first message in a Slack thread, a Telegram chat, or a fresh web conversation — never pre-allocated. |
| **Project** | One folder plus its defaults. The folder is the `cwd` the agent subprocess runs in. Several sessions can share one project. |
| **Preset** | Reusable agent instructions — one `agent.md` file. The built-in `default` preset is the fallback when a session has no project; it cannot be deleted, only edited. |
| **Provider** | Which CLI runs, and under which credentials. Two `claude/...` profiles with different tokens can coexist alongside codex and gemini. |

The folder is the agent's world. Whatever is in the project folder — files you put there, files the agent writes via Bash — is what it can see.

## Projects: managed vs custom

- **Managed** — wick creates an empty folder under `~/.<app>/agents/projects/<id>/files/` and deletes it when the project is deleted.
- **Custom** — you point the project at an existing path anywhere on disk. Wick does **not** delete it on project delete.

Use custom when the agent should work on a real repo you already have; managed when you want a scratch space wick owns.

## Channels

The same agent is reachable from Slack, Telegram, and the web UI. Each thread, chat, or web conversation becomes its own session, so context does not bleed between them.

The web UI is always on. Slack and Telegram need a bot token and access-control config under the Channels page, where you also set the default project new sessions land in.

## The pool

A pool caps how many agent subprocesses run at once and FIFO-queues the rest. Idle sessions are killed and later resumed transparently, so a long-lived conversation does not hold a process hostage.

What this means practically: a queued session is not stuck, it is waiting. Check the Overview page (active / max / queue) before concluding something has hung.

## Scheduled messages

A message can be injected into a session later — one-shot or recurring — without involving the workflow engine. Either the agent schedules it over MCP, or a human does from the UI. The Scheduled page is a cross-session monitor with inline pause / resume / cancel.

Reach for this when the need is "say this again later in this same conversation". Reach for a workflow when the need is a multi-step pipeline.

## Where the pages are

Everything lives under `/tools/agents`:

| Page | What you do there |
|---|---|
| **Overview** | Pool stats, running list, recent sessions. |
| **Sessions** | Open a session; tabs for Conversation, Commands (gate audit), Approvals, Raw. |
| **Projects** | Create / delete projects. |
| **Presets** | Edit reusable agent instructions. |
| **Providers** | Per-instance status: binary path, version, env vars, Rescan. |
| **Skills** | Browse, preview, sync, delete skill files across provider dirs. |
| **Channels** | Slack + Telegram config. |
| **Scheduled** | Monitor scheduled messages. |

## Storage

All agent state is on disk under `~/.<app>/agents/` — sessions, projects, presets, workflows. There is no DB migration to worry about: backup is `tar`, and a restart re-scans the tree.

`<app>` is whatever the binary's name resolves to, so a dev build named `wick-lab` keeps its state in `~/.wick-lab/` and never collides with a release build's `~/.wick/`.

## Skills

Skills are folders holding a `SKILL.md`, discovered from each provider's skill directory (`~/.claude/skills`, `~/.codex/skills`, `~/.gemini/skills`, `~/.<app>/skills`, and the shared `~/.agents/skills`). The Skills page syncs them across those directories.

The `wick` provider loads them differently from the CLIs: because it runs in-process, it injects a compact catalog — name, description, and the `SKILL.md` path — into its system prompt, then reads a skill's full body on demand when it actually engages one.
