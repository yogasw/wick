# AI Quickstart

Every project scaffolded by `wick init` includes `agent.md` — your AI agent reads it first and already knows the file layout, naming rules, and framework conventions.

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

## How Claude uses agent.md

When you open a Wick project in Claude Code, it reads `agent.md` first. This file tells Claude:

- Where to put new tools and jobs
- How to name files and packages
- How `Register` and `Run` funcs must be shaped
- How to register in `main.go`
- What `wick:"..."` tags are available for Config structs

You don't need to explain the framework in your prompts — just describe what the tool or job should **do**.

## Tips for better results

- **Be specific about inputs and outputs**: "takes a URL, fetches it, returns the response body" is better than "fetch something"
- **Mention config knobs explicitly**: "needs a config field for the API key" so Claude adds it to the Config struct with `secret;required`
- **Specify visibility**: "make it private" or "public, visible to everyone"
- **Mention the category**: Claude will pick or create a tag group accordingly
- **For jobs, give the cron schedule**: "every hour", "daily at midnight UTC", "every 15 minutes"
