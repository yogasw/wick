# Skill: node-builder

Status: **superseded** — pattern node creation sekarang udah live di
[`.claude/skills/workflow-node-module/SKILL.md`](../../../.claude/skills/workflow-node-module/SKILL.md)
(project-scoped skill, auto-trigger). Doc ini tetap disimpan sebagai
blueprint historis + tracker untuk skill `workflow-builder` companion.
Update terakhir: 2026-05-18.

---

## TODO

- [x] Skill aktif → `.claude/skills/workflow-node-module/SKILL.md` (project-scoped, full UI+engine pattern). Sumber kebenaran.
- [ ] Tambah skill `workflow-builder` (sibling) — bantu compose multi-node workflow dari natural language
- [ ] Validate workflow-builder: 1 sample run end-to-end (drop "build a workflow that triggers on Slack, classifies intent, routes to GitHub" → AI hasilkan workflow.yaml lengkap)
- [ ] Iterate trigger phrases setelah real-world usage

---

## Konteks

Wick workflow editor pakai plugin arch
([plugin-arch.md](plugin-arch.md)): 1 node baru = sepasang folder:

1. **Engine** (`internal/agents/workflow/nodes/<type>.go`) — executor + exported schema struct + `Descriptor()`.
2. **UI plugin** (`internal/tools/agents/workflow/nodes/<type>/`) — `meta.go` + `inspector.templ` (one-liner `ArgForm`) + `inspector.js`.

Schema reflected via `entity.StructToConfigs(<Type>Schema{})` di kedua sisi — single source of truth.

Reference lengkap step-by-step ada di
[workflow-node-module SKILL.md](../../../.claude/skills/workflow-node-module/SKILL.md).
Skill aktif sebagai auto-trigger pas user touch path
`internal/agents/workflow/nodes/**` atau
`internal/tools/agents/workflow/nodes/**`.

---

## Skill spec (untuk file `SKILL.md`)

````markdown
---
name: node-builder
description: Generate boilerplate for a new wick workflow node — Go module (meta.go + executor), templ partial, vanilla JS module. Use when user asks to "add a new workflow node", "buat node baru di workflow", "tambah custom node", or "wick add node X". Outputs the full folder structure ready to compile.
---

# wick workflow node-builder

Skill ini bantu user bikin node baru di wick workflow editor. Pattern
follows `internal/docs/workflow/plugin-arch.md` — per-node folder
with meta.go, inspector.templ, inspector.js.

## Trigger phrases

- "add a new workflow node"
- "buat node baru di workflow"
- "tambah node X ke workflow"
- "wick new node"
- "scaffold workflow node"

## Inputs needed

Tanya user 4 hal kalau belum dikasih:

1. **Node type slug** (e.g. `stripe_charge`, `redis_get`, `slack_react`)
   - Lowercase, snake_case (underscore OK after validator update)
   - Singular, descriptive
2. **Palette section** — "AI" / "Action" / "Logic" / new section
3. **Purpose 1-liner** — apa yg node lakuin (untuk Hint label)
4. **Input fields** — list field user perlu set di inspector
   (e.g. "api_key" secret, "amount" number, "currency" enum)

## Output

Generate 6 file:

### 1. `internal/agents/workflow/types.go` (patch)

Add NodeType const + Node struct fields. Patch, jangan rewrite:

```go
const Node<Type> NodeType = "<type>"
```

```go
// <type> — per-type field
<Field> <GoType> `yaml:"<field>,omitempty"`
```

### 2. `internal/agents/workflow/parse/parse.go` (patch)

Tambah case di `validateNodeBody`:

```go
case workflow.Node<Type>:
    if n.<RequiredField> == "" {
        r.Errors = append(r.Errors, Error{Path: path + ".<field>", Message: "is required"})
    }
```

### 3. `internal/agents/workflow/nodes/<type>.go` (new)

Engine-side executor — implement `workflow.Executor`:

```go
package nodes

import (
    "context"
    "fmt"
    "github.com/yogasw/wick/internal/agents/workflow"
    "github.com/yogasw/wick/internal/agents/workflow/template"
)

type <Type>Executor struct{}

func New<Type>Executor() *<Type>Executor { return &<Type>Executor{} }

func (e *<Type>Executor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
    // Render templated fields
    // Validate runtime args
    // Do work (HTTP call, DB query, whatever)
    // Return NodeOutput{Result: ..., Fields: {...}}
}
```

Register di `internal/agents/workflow/setup/manager.go`:

```go
eng.Register(workflow.Node<Type>, nodes.New<Type>Executor())
```

### 4. `internal/tools/agents/workflow/nodes/<type>/meta.go` (new)

Plugin module — palette + canvas codec:

```go
package <type>

import (
    "github.com/a-h/templ"
    wf "github.com/yogasw/wick/internal/agents/workflow"
    registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.Node<Type> }
func (m *module) PaletteSection() string { return "<Section>" }

func (m *module) PaletteItem() registry.PaletteItem {
    return registry.PaletteItem{
        Type:  string(wf.Node<Type>),
        Label: "<label>",
        Dot:   "bg-<color>-500",
        Hint:  "<hint>",
    }
}

func (m *module) Render() registry.NodeRender {
    return registry.NodeRender{
        Head: "<label>", Hint: "<hint>", CSSType: "<type>",
        Inputs: 1, Outputs: 1,
    }
}

func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
    return map[string]any{
        "<field>": n.<Field>,
        // ... per-field projection
    }
}

func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
    field, _ := inner["<field>"].(string)
    return wf.Node{ID: id, Type: wf.Node<Type>, <Field>: field}
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }
func (m *module) InspectorScript() string { return "<type>/inspector.js" }
```

### 5. `internal/tools/agents/workflow/nodes/<type>/inspector.templ` (new)

```go
package <type>

templ Inspector() {
    <div class="wf-inspector-panel hidden" data-node-type="<type>">
        <label class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1 mt-3"><Field></label>
        <input id="ins-<type>-<field>" type="text" class="wf-input"/>
        <!-- repeat per field -->
    </div>
}
```

### 6. `internal/tools/agents/workflow/nodes/<type>/inspector.js` (new)

```js
(function () {
    'use strict';
    const mod = {
        meta: {
            kind: '<type>', head: '<label>', hint: '<hint>',
            cssType: '<type>', inputs: 1, outputs: 1,
            defaults: { /* seed fields */ },
        },
        onDrop(data) { /* set defaults if not present */ },
        hydrate(inner) {
            document.getElementById('ins-<type>-<field>').value = inner.<field> || '';
        },
        save(inner) {
            inner.<field> = document.getElementById('ins-<type>-<field>').value.trim();
        },
    };
    window.WickNodes = window.WickNodes || {};
    window.WickNodes['<type>'] = mod;
})();
```

### 7. `internal/tools/agents/workflow/nodes/all/all.go` (patch)

Add blank import:

```go
_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/<type>"
```

## Post-generation steps

Tell user untuk run:

```bash
templ generate -path internal/tools/agents/workflow/nodes/<type>/inspector.templ
go build ./internal/... && go test ./internal/agents/workflow/...
```

Lalu refresh editor browser, drop node dari palette.

## Common patterns

- **Templated args**: use `template.Render(n.<Field>, rc.RenderCtx())` di executor
- **Fixed vs Expression toggle**: honor `n.ArgModes[<key>] == "fixed"` di executor — skip `template.Render`, pass value verbatim. Pattern di [`nodes/http.go::renderHTTPField`](../../agents/workflow/nodes/http.go)
- **Conditional inspector fields**: pakai `visible_when=field:value` (single) atau `visible_when=field:a|b|c` (pipe-separated OR) di tag schema — UI hide row sampai dependency match
- **Map-shaped fields (headers, query)**: tag `kvlist=name|value` → row editor table. Inspector glue convert kvlist JSON ↔ `map[string]string`. Per-value selalu di-template-render (no per-row toggle)
- **Secret field**: kalau field perlu encryption, refer `internal/docs/encrypted-fields.md`
- **Branching**: if node outputs verdict for case-based routing, implement `IsBranchSource()` di NodeType + return `Verdict` di NodeOutput
- **Long-running**: use `ctx` cascade; engine wraps with `MaxDurationSec` (see [pool.md](pool.md) timeout strategy)

## Reference

- Plugin arch full reference: `internal/docs/workflow/plugin-arch.md`
- Working example: `internal/tools/agents/workflow/nodes/session_init/`
- Engine concepts: `internal/docs/workflow/06-graph-engine.md`
- Validator: `internal/agents/workflow/parse/parse.go`
- Conventions:
  - Generic naming (no "qiscus") — `internal/docs/workflow/02-principles.md`
  - No AI attribution in commits (per CLAUDE.md memory)
  - Use Bash tool first, PowerShell fallback (per CLAUDE.md memory)

## Common mistakes to avoid

- Forgetting `_ "..."` blank import di `all/all.go` → module ngak ke-register
- Node ID dgn uppercase or special chars → validator `[a-z0-9_-]+` reject
- Hardcode session/state across runs — use `rc.RunID` di sessionID kalau perlu per-run scoping
- Edit `_templ.go` files — itu generated, edit `.templ` then regenerate
````

---

## Companion skill: `workflow-builder`

Skill terpisah yg compose multi-node workflow YAML dari natural
language. Pattern: skill ini ngerti node catalog, MCP ops
(`workflow_add_node`, `workflow_connect`, `workflow_validate`), dan
generate complete workflow.yaml dari intent like "build a workflow
that triggers on Slack message, classifies intent as bug/question,
then routes bug to GitHub issue create vs question to docs search".

Deferred — defined separately after `node-builder` validated.

---

## Why not auto-generate everything?

Considered: schema-driven node where module declares YAML schema
(reflection), validator + executor + UI auto-wire. Rejected
short-term because:

1. **Boilerplate not the bottleneck** — most node effort is the
   executor logic + edge case handling, not the wiring. Skill
   accelerates the wiring; executor still needs human/AI thought.
2. **Inspector UX varies wildly per node** — pure schema-driven UI
   loses the inspector-specific affordances (Auto button on
   session_init, picker chips on channel filters, code editor for
   transform). Generic form generator falls short.
3. **Premature abstraction** — pattern stable across 1 example
   (session_init); needs 3+ before extracting common framework.

Schema-driven jadi candidate kalau 5+ nodes pakai plugin pattern dan
~80% inspector UIs converge ke same shape.
