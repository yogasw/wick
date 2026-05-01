---
layout: home

hero:
  name: "Wick"
  text: "Just Prompt. AI Does the Rest."
  tagline: Stop copy-pasting AI output into black-box editors. Wick gives AI a real Go project — you own everything it builds.
  image:
    src: /logo.svg
    alt: Wick
  actions:
    - theme: brand
      text: Start with AI
      link: /guide/ai-quickstart
    - theme: alt
      text: Manual Setup
      link: /guide/getting-started

features:
  - icon: 🤖
    title: AI Is the Primary User
    details: Wick is designed for AI agents, not humans. Every convention, file name, and pattern is optimized so Claude knows exactly what to create — no exploration, no guessing.
  - icon: 🗂️
    title: Git Is the Control Plane
    details: No drag-and-drop UI to version. Every tool and job AI creates is real code in real files. `git diff` to review, `git revert` to undo. You own everything.
  - icon: 🧰
    title: Tools, Jobs, & Connectors
    details: Say "add a Slack notifier job" or "add a GitHub connector for our LLM agent". Claude creates the file, registers it, wires the config — for humans, schedulers, and LLMs alike.
  - icon: 🤖
    title: LLM-Ready via MCP
    details: Expose any connector to Claude, Cursor, and other MCP clients. Built-in OAuth 2.1 + Personal Access Tokens, per-call audit log, no protocol code on your side.
  - icon: 👀
    title: See Everything That Was Built
    details: Git history IS your tool inventory. Who built what, when, and why — no separate dashboard or admin panel to maintain.
  - icon: 🔐
    title: SSO & Access Control
    details: SSO built in. Group tools with tags, set visibility per tool. Configured from admin — no redeploy needed.
  - icon: ⚙️
    title: Live Config, No Redeploy
    details: Declare a typed Config struct. Fields become admin-editable rows. Secrets, URLs, toggles — updated live without touching code.
---
