# template

Agent-first Go service scaffold built on [wick](https://github.com/yogasw/wick).

## Quick start

```
wick setup
wick dev
```

Open:
- Tool: http://localhost:8080/tools/convert-text
- Job (operator): http://localhost:8080/jobs/auto-get-data
- Job (admin): http://localhost:8080/manager/jobs/auto-get-data
- Connector (admin): http://localhost:8080/manager/connectors/crudcrud
- MCP install snippets: http://localhost:8080/profile/mcp (after first login)

## For AI agents

This repo is designed to be driven by AI coding agents. Before editing:

1. Read [AGENTS.md](./AGENTS.md) — repo layout, naming rules, `wick` commands.
2. When creating or editing a tool/job, invoke the [`tool-module`](./.claude/skills/tool-module/SKILL.md) skill — it enforces the module contract and points you at the canonical examples (`tools/convert-text/`, `jobs/auto-get-data/`) you should read before writing new code.
3. When creating or editing a connector, invoke the [`connector-module`](./.claude/skills/connector-module/SKILL.md) skill — connectors are LLM-facing modules exposed over MCP. The canonical example is [`connectors/crudcrud/`](./connectors/crudcrud/connector.go).
4. To refresh skills after a wick upgrade: `wick skill sync` (replaces `./.claude/skills/*` with the bundled version; updates `AGENTS.md` skill table if still in default shape).
5. For wick framework APIs not documented in the skill, fetch <https://yogasw.github.io/wick/llms.txt>.

## Layout & conventions

See [AGENTS.md](./AGENTS.md).
