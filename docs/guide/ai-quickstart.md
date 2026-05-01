# AI Quickstart

Every project scaffolded by `wick init` includes `AGENTS.md` — your AI agent reads it first and already knows the file layout, naming rules, and framework conventions.

## Setup a new project

<PromptBox />

Open the scaffolded project in your editor and start prompting.

---

## Sample Prompts

Copy and paste these directly into Claude Code.

### Create a new tool

```
add a tool called "base64" that encodes and decodes text.
it should have two tabs: encode and decode.
make it public and add it to the Text category.
```

```
create a tool called "json-formatter" that pretty-prints and minifies JSON.
add a config option for default indentation (2 or 4 spaces).
```

```
build a tool called "color-picker" that converts between HEX, RGB, and HSL.
show a live preview of the selected color.
```

### Create a new background job

```
add a job called "slack-digest" that posts a daily summary to a Slack webhook.
it needs a config field for the webhook URL (required, url type) and channel name.
schedule it to run every day at 9am UTC.
```

```
create a job called "db-cleanup" that deletes records older than 30 days from
the audit_logs table. add a config field for retention days (number, default 30).
```

### Create a new connector (LLM-facing via MCP)

```
add a connector for the GitHub REST API with operations:
list_repos, get_repo, list_issues, create_issue (destructive),
close_issue (destructive). credential is a personal access token
(secret, required). base url defaults to https://api.github.com.
```

```
add a connector for our internal Loki at https://loki.example.com.
one operation: query (LogQL string input). add a token field (secret).
```

```
add a connector for Slack with one operation: send_message (destructive).
inputs are channel and text. credential is a bot token (secret, required).
```

::: tip List operations explicitly
Tell Claude every operation by name plus whether it's destructive. The destructive flag defaults the per-row toggle off so admins must opt in — don't let Claude guess.
:::

### Add an external link card

```
add an external link to our Grafana dashboard at https://grafana.example.com.
name it "Grafana", icon "📊", category "Monitoring".
```

### Duplicate a tool with different config

```
register a second instance of "convert-text" with key "convert-text-jp",
name "Convert Text (JP)", and seed InitText as "こんにちは世界".
```

### Add a new tag group

```
add a new tag called "Finance" for grouping finance-related tools.
set IsGroup true and SortOrder 50.
use it as the DefaultTag for any finance tools.
```

### Modify an existing tool

```
add a "copy to clipboard" button to the convert-text tool result area.
use vanilla JS only, no CDN.
```

```
add a config option to the base64 tool called "url_safe" (checkbox, default false)
that switches between standard and URL-safe base64 encoding.
```

---

## How Claude uses AGENTS.md and skills

When you open a Wick project in Claude Code, it reads `AGENTS.md` first. That file points at the bundled skills in `./.claude/skills/`:

- **`tool-module`** — enforces the tool/job contract, mandates a clarify + plan loop before writing code, and points Claude at the canonical examples (`tools/convert-text/`, `jobs/auto-get-data/`).
- **`connector-module`** — enforces the connector contract: typed `Configs` + per-op `Input` structs, `http.NewRequestWithContext` rule, destructive-opt-in model. Canonical example: `connectors/crudcrud/`.
- **`design-system`** — locks down colors, spacing, typography, and dark/light pairing.

Together they tell Claude:

- Where to put new tools and jobs
- How to name files and packages
- How `Register` and `Run` funcs must be shaped
- How to register in `main.go`
- What `wick:"..."` tags are available for Config structs
- The correct design tokens for any UI

You don't need to explain the framework in your prompts — just describe what the tool or job should **do**. The skill will make Claude ask clarifying questions and propose a plan before it writes any files.

### Keeping skills up to date

After upgrading `wick`, pull in the latest bundled skills:

```bash
wick skill sync
```

This replaces `./.claude/skills/tool-module/` and `./.claude/skills/design-system/` with the versions shipped in your current wick binary. It also refreshes the skill table in `AGENTS.md` if the table still matches the default shape, and creates `AGENTS.md` from the template if it's missing. Customized `AGENTS.md` files are left alone.

## Tips for better results

- **Be specific about inputs and outputs**: "takes a URL, fetches it, returns the response body" is better than "fetch something"
- **Mention config knobs explicitly**: "needs a config field for the API key" so Claude adds it to the Config struct with `secret;required`
- **Specify visibility**: "make it private" or "public, visible to everyone"
- **Mention the category**: Claude will pick or create a tag group accordingly
- **For jobs, give the cron schedule**: "every hour", "daily at midnight UTC", "every 15 minutes"
- **For connectors, list operations explicitly**: "with operations: a, b, c (destructive)" — saves Claude from inventing or omitting ops, and the destructive flag is load-bearing for safety
