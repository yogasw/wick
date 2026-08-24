---
name: wick-custom-connectors
description: Use when the user wants wick to reach an API it has no connector for yet — "buatin connector untuk X", "add a connector for this API", "wrap this cURL", "register our internal MCP server", "add an operation to that connector I made", or asks why a connector they built shows nothing. Covers the custom-connector management surface (def_schema, def_validate, def_create, mcp_register, instance_create, and the rest), the plan-then-confirm workflow those operations require, who is allowed to call them, and the cases that still need the dashboard.
---

# Building a custom connector

A **custom connector** wraps one external API without Go code, a rebuild, or a
redeploy. It is a definition stored in the database that wick replays into the
same registry as built-in connectors, so afterwards the two are
indistinguishable: same `tool_id` shape, same encrypted fields, same audit
trail, same tag-based access control.

Use `wick-connectors` for CALLING connectors. This skill is about creating one.

## You can build it from chat

The **`custom-connector`** connector exposes the whole lifecycle as operations,
so no one has to open the dashboard. Reach it the usual way — `wick_list` /
`wick_search` to find its instance, then `wick_get` for an operation's schema.

| Operation | Does |
|---|---|
| `def_schema` | the full draft contract — **read this first** |
| `def_validate` | dry-run a draft; persists nothing |
| `def_create` | create from a manual draft |
| `def_list` / `def_get` | what exists, and one definition's current shape |
| `def_update` | replace a definition wholesale (key is immutable) |
| `def_resync` | re-fetch an MCP server's tool list |
| `def_set_disabled` / `def_delete` | serve zero ops, or remove entirely |
| `mcp_register` | register an external MCP server as a connector |
| `mcp_set_excluded` | change which of its tools are exposed |
| `instance_list` / `instance_create` | the rows that hold credentials |
| `instance_delete` / `instance_set_disabled` | remove or park a row |

There is deliberately **no cURL-parse operation**. Converting a cURL command,
a `fetch()` snippet, or API docs into the draft shape is your job — that is
what an LLM is for, and it keeps the surface small.

## The workflow is not optional

`def_create` and `mcp_register` both state it in their own descriptions, and
they mean it:

1. **`def_schema`** — read the contract. Widgets, template syntax, limits,
   validation rules, categories, a worked example. Do not draft from memory;
   this skill deliberately does not copy that reference, because a stale copy
   is worse than a lookup.
2. **Build the draft** from whatever the user gave you.
3. **`def_validate`** — same validation `def_create` runs, plus a
   key-availability check. Free, persists nothing.
4. **Present the plan and wait.** Name, key, icon, description; every config
   field with its widget and its secret/required flags; every operation with
   its description, inputs, and request recipe; and the decisions you made for
   them — single vs multi-instance, which ops you marked destructive, which
   fields you made secret, the category.
5. **`def_create`** only after they say yes.
6. **`instance_create`** — a definition serves nothing without a row. Config
   values go in afterwards, via the dashboard or the `wickmanager` connector's
   `connector_set_config`.

Step 4 is the one worth defending. A connector is a standing capability with
credentials attached: the key can never be renamed, a missed `destructive` flag
means an agent can fire a delete by accident, and a config field that should
have been `secret` has already been stored in the clear. All three are cheap to
fix in a plan and expensive afterwards.

## Who can use it

The operations are **scoped, not admin-gated**: admins manage every definition,
everyone else manages the ones they created. A non-admin can build and own a
connector.

But the `custom-connector` row itself ships with the **System tag**, which is
admin-only — it is wick's own management plane. So a non-admin who should build
connectors needs that access granted first (`/admin/tags`). If they cannot see
the connector in `wick_list`, that is why; say so rather than assuming they lack
permission in principle.

Note what `def_create` does to visibility: it creates an access tag
`custom:<key>` that is **admin-only until granted**. The person who just built
the connector may not see it in `wick_list` afterwards, and that is the tag, not
a failure. Point them at `/admin/tags` → `custom:<key>`, or tell them to remove
the tag from the instance to open it to every approved user.

## Is a custom connector even the right answer

First match wins:

| Situation | Use |
|---|---|
| A built-in connector already covers the API | that one — check `wick_list` first |
| One ad-hoc call, no reuse | the built-in **HTTP / REST** connector |
| An agent should be limited to specific endpoints and operations | **custom connector** |
| Needs pagination, retries, multi-step logic, response reshaping | a Go connector or a plugin |
| The team already hosts an MCP server | **`mcp_register`** |

That fourth row is the common misread. A custom operation is **one request**,
response passed through as-is: it cannot loop, follow a cursor, or transform the
payload. Say so before building rather than shipping something that half-works.

## MCP servers

`mcp_register` tests `initialize` + `tools/list` with the given auth and
**refuses to save when the test fails**, so a broken registration never lands.
One server is one connector; **every listed tool becomes an operation** minus
the names you exclude, and tools added upstream appear after `def_resync`.

Confirm with the user first: label (it becomes the connector name and key), URL,
auth scheme and where the credential comes from, icon, description, and which
tools to exclude.

Streamable HTTP only — a stdio server needs an HTTP sidecar
(`mcp-proxy`, `supergateway`) in front. Auth schemes here are `none`, `bearer`,
`custom_header`, and `sso`.

## What still needs the dashboard

- **OAuth-scheme MCP servers.** They need an interactive browser login, so
  `mcp_register` rejects the scheme outright. Send the user to
  **Connectors → + New connector → From MCP server**.
- **Filling credential values**, when you would otherwise be handling the
  user's secrets. `connector_set_config` exists, but a human pasting their own
  token into the instance page is usually the better path.
- **The AI paste tab**, if they would rather have wick extract the shape from a
  paste than have you do it. Same result, one LLM call inside wick instead.

## Editing takes a reload

`def_update` replaces the stored definition wholesale and reloads the live
module immediately — fetch the current shape with `def_get` first, or you will
drop whatever you did not resend. The key cannot change.

`def_set_disabled` keeps the pages and rows but serves zero operations.
`def_delete` removes the definition and its instances; run history survives for
audit, and the `custom:<key>` tag survives too, so re-creating the same key
restores prior grants.

## Limits worth stating up front

- One request per operation. No pagination, retries, or response reshaping.
- 1 MB per rendered template; responses are read up to 4 MB.
- Rendered URLs must be `http://` or `https://`.
- No scripting — templates are logic-less beyond the whitelisted functions.
- Non-2xx surfaces as an error with a body snippet in the run history.
- A missing template key is a hard error, not an empty string: `{{.cfg.api_keyy}}`
  fails the call and names the typo. Renaming a config key means updating every
  template that referenced it.

`def_schema` carries the authoritative version of all of this.
