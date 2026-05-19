---
outline: deep
---

# `http`

Outbound HTTP request. URL / headers / query / body rendered as Go templates. Retry policy + `parse_response` shape control.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/http.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/http.go) |
| **When to use** | Direct external API calls without a typed connector module. For repeated calls to the same API, write a [connector](/guide/connector-module) instead — typed inputs + audit log + tag visibility. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `method` | dropdown | ✅ | `GET` / `POST` / `PUT` / `PATCH` / `DELETE`. |
| `url` | template | ✅ | Full URL. Rendered as Go template — pull values from upstream nodes via <code v-pre>{{.Node.x.y}}</code> or <code v-pre>{{.Event.Payload.z}}</code>. |
| `headers` | kvlist (templated) | | Each value is rendered as a Go template — e.g. <code v-pre>Authorization: Bearer {{.Node.login.token}}</code>. |
| `query` | kvlist (templated) | | Query string parameters. Each value templated. |
| `body` | textarea (template) | | Request body as string. Use YAML block scalar `\|` for multiline JSON. Visible only for `POST`/`PUT`/`PATCH`/`DELETE`. |
| `parse_response` | dropdown | | `raw` (default) / `json` / `bytes`. |
| `timeout_sec` | int | | Request timeout in seconds. Default 30. |

## Output

| Field | Type | What |
|---|---|---|
| `status` | int | HTTP status code. Branch on it via a downstream `branch` node (<code v-pre>{{.Node.x.status}} >= 400</code>). |
| `body` | string | Response body as string. Always populated regardless of `parse_response`. |
| `headers` | map | Flat map of response headers — first value per key. |
| `json` | any | Parsed JSON body. Populated when `parse_response: json` or unset and the body is valid JSON. Use <code v-pre>{{.Node.x.json.&lt;field&gt;}}</code>. |
| `bytes` | bytes | Raw bytes — populated only when `parse_response: bytes`. |

## Example

```yaml
- id: file_ticket
  type: http
  method: POST
  url: https://api.example.com/tickets
  headers:
    Content-Type: application/json
    Authorization: Bearer {{.Env.TICKETS_TOKEN}}
  body: |
    {
      "title": "{{jsonEscape (index .Event.Payload "text")}}",
      "user":  "{{jsonEscape (index .Event.Payload "user")}}"
    }
  parse_response: json
```

## Templates: escape your strings

The most common mistake is putting raw user text into a JSON body without escaping:

```yaml
body: '{"text": "{{.Event.Payload.text}}"}'    # ❌ quotes in text break JSON
body: '{"text": "{{jsonEscape .Event.Payload.text}}"}'   # ✅
```

The `jsonEscape` helper escapes `"`, `\`, and control characters. For multiline payloads use the YAML block scalar `|` so newlines render predictably.

## Pair with

- [`connector`](./connector) — typed alternative once you call the same API repeatedly.
- [`transform`](./transform) — reshape `body` / `json` between calls.
- [`branch`](./branch) — route on `status`.
