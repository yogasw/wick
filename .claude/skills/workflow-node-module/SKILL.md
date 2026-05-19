---
name: workflow-node-module
description: Use for ANY work on a workflow node type — creating a new node executor under internal/agents/workflow/nodes/, refactoring/improving an existing one, adding fields to a node's schema, or wiring the executor into setup/manager.go. Covers the full executor contract — workflow.Executor interface, engine.NodeDescriptor for MCP catalog, schema reflection via wick:"..." tags, output field documentation, the engine.Register/RegisterWithDesc dispatch, and the goroutine-context discipline shared with connectors. Also mandates the "Descriptor() method is the source of truth" rule — schema and output docs live next to the executor, never in mcp/.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "internal/agents/workflow/nodes/**"
  - "internal/agents/workflow/engine/**"
  - "internal/agents/workflow/setup/manager.go"
  - "internal/agents/workflow/types.go"
  - "internal/agents/workflow/executor.go"
  - "internal/agents/workflow/template/**"
  - "internal/agents/workflow/integration/schema.go"
  - "internal/agents/workflow/mcp/mcp.go"
---

# Workflow Node Module — wick core

Activate this skill whenever the user touches a workflow node type — creating, improving, fixing, or adding fields. When editing an existing node, audit it against the rules below and bring it up to spec as part of the change.

## Mental model

A node has two halves that share one schema:

| Half | Lives in | Touches |
|---|---|---|
| **Engine** (executor) | `internal/agents/workflow/nodes/<type>.go` | runtime — receives `workflow.Node`, returns `NodeOutput` |
| **UI plugin module** (palette + inspector) | `internal/tools/agents/workflow/nodes/<type>/` | canvas — palette entry, drawflow codec, inspector partial, JS module |

The shared schema struct (exported type, e.g. `HTTPSchema`) lives in the engine package and is reflected by **both**:
- engine `Descriptor().Schema` → `integration.StructSchema(HTTPSchema{})` → MCP `workflow_node_types`
- UI inspector partial → `entity.StructToConfigs(HTTPSchema{})` → `wfview.ArgForm(rows)` → editor HTML

Adding a new node type touches:

1. `internal/agents/workflow/types.go` — `NodeType` constant + flat `Node` struct fields
2. `internal/agents/workflow/nodes/<type>.go` — executor + exported schema struct + `Descriptor()`
3. `internal/agents/workflow/setup/manager.go` — one `eng.Register` line
4. `internal/tools/agents/workflow/nodes/<type>/meta.go` — palette + codec module
5. `internal/tools/agents/workflow/nodes/<type>/inspector.templ` — inspector partial (one-liner that calls ArgForm)
6. `internal/tools/agents/workflow/nodes/<type>/inspector.js` — hydrate/save glue
7. `internal/tools/agents/workflow/nodes/all/all.go` — blank import the new subpackage
8. `internal/tools/agents/workflow/nodes/static.go` — extend `//go:embed all:<type>` for the JS file

The catalog flow:

```
eng.Register(workflow.NodeFoo, nodes.NewFooExecutor())
  → engine.Register checks if executor implements engine.Describer
  → calls Descriptor() → stores engine.NodeDescriptor in Engine.Descriptors[t]
  → mcp.NodeTypesCatalog(eng) builds workflow_node_types response from Descriptors map
```

## Before you build

Lock down the contract before writing code:

| What to gather | Why |
|---|---|
| **What does this node DO** at runtime | Decides whether it should be a new node type at all, or just a new arg to an existing one (e.g. add a flag to http, not a new "http_with_retry" type) |
| **Inputs** the node accepts | Becomes the schema struct fields with `wick:"..."` tags |
| **Outputs** the node produces (field names + types) | Becomes the `Output` map in `Descriptor()`. References as `{{.Node.<id>.<field>}}` downstream |
| **Side effects** — pure compute? network? mutate dataset? | Decides whether the executor needs `c.Context()` discipline (any network/blocking call MUST honor context) |
| **Failure modes** — what raises an error vs returns empty | Drives error wrapping in `Execute` |

## File layout

Default — one file under `internal/agents/workflow/nodes/`:

```
internal/agents/workflow/nodes/
  myop.go    # Executor struct + NewMyOpExecutor + Execute + schema struct + Descriptor()
```

Pattern (read [`http.go`](../../../internal/agents/workflow/nodes/http.go) as canonical):

```go
package nodes

import (
    "context"

    "github.com/yogasw/wick/internal/agents/workflow"
    "github.com/yogasw/wick/internal/agents/workflow/engine"
    "github.com/yogasw/wick/internal/agents/workflow/integration"
    "github.com/yogasw/wick/internal/agents/workflow/template"
)

// MyOpExecutor performs <what it does>.
type MyOpExecutor struct {
    // dependencies injected via constructor (registry pointers, http clients, etc.)
}

// NewMyOpExecutor wires the executor.
func NewMyOpExecutor() *MyOpExecutor { return &MyOpExecutor{} }

// Execute runs the op described by node n.
func (e *MyOpExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
    rctx := rc.RenderCtx()
    // 1. validate required fields on n
    // 2. render any template-bearing strings via template.Render(s, rctx)
    // 3. call into pure logic or external I/O — honor ctx
    // 4. return workflow.NodeOutput{Fields: map[string]any{...}}
    _ = rctx
    return workflow.NodeOutput{}, nil
}

// myOpSchema reflects into JSON schema for workflow_node_types — single
// source of truth for AI consumers and the inspector UI.
type myOpSchema struct {
    Required  string `wick:"required;key=required;desc=Mandatory input"`
    Optional  string `wick:"key=optional;desc=Optional input"`
    Enum      string `wick:"key=mode;dropdown=a|b|c;desc=Pick one"`
    Multiline string `wick:"key=body;textarea;desc=Multi-line input"`
}

// Descriptor exposes schema + docs for the MCP catalog.
func (e *MyOpExecutor) Descriptor() engine.NodeDescriptor {
    return engine.NodeDescriptor{
        Description: "Action verb. Returns <output shape>.",
        WhenToUse:   "Use when <condition>; prefer X over this when <other condition>.",
        Example:     "- id: myop\n  type: my_op\n  required: foo\n  mode: a",
        Schema:      integration.StructSchema(myOpSchema{}),
        Output: map[string]string{
            "result": "string — rendered output",
        },
    }
}
```

## Wire into the engine

Two locations:

### 1. `internal/agents/workflow/types.go` — add `NodeType` constant

```go
const (
    // existing types …
    NodeMyOp NodeType = "my_op"
)
```

Also add any new fields you read in `Execute` to the `Node` struct (with `yaml:"…"` tag matching your schema key).

### 2. `internal/agents/workflow/setup/manager.go` — register

```go
eng.Register(workflow.NodeMyOp, nodes.NewMyOpExecutor())
```

`Register` auto-detects the `Describer` interface and captures the descriptor — no separate `RegisterWithDesc` call needed.

Exception: when **one executor instance serves multiple node types** (like `DatasetExecutor` handling 7 dataset_* types), use `RegisterWithDesc(t, exec, desc)` and provide a helper that switches on the type:

```go
for _, t := range []workflow.NodeType{NodeFoo, NodeBar, NodeBaz} {
    eng.RegisterWithDesc(t, exec, nodes.FooDescriptor(t))
}
```

## Wire into the editor UI

The UI side lives under `internal/tools/agents/workflow/nodes/<type>/` — palette entry, drawflow codec, inspector partial, and inspector JS module. Each per-node folder is a Go subpackage whose `init()` registers a module with the editor registry; the workflows editor iterates the registry to render the palette and dispatch hydrate/save by `data-node-type`.

Canonical example to read: [`internal/tools/agents/workflow/nodes/http/`](../../../internal/tools/agents/workflow/nodes/http/) (full schema-driven inspector via `ArgForm` + kvlist + Fixed/Expression toggle). Simpler example: [`session_init/`](../../../internal/tools/agents/workflow/nodes/session_init/) (hand-coded inputs).

### `meta.go` — palette + drawflow codec

```go
package mytype

import (
    "github.com/a-h/templ"

    wf "github.com/yogasw/wick/internal/agents/workflow"
    registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType    { return wf.NodeMyType }
func (m *module) PaletteSection() string   { return "Action" } // AI | Action | Logic | Triggers
func (m *module) PaletteItem() registry.PaletteItem {
    return registry.PaletteItem{
        Type:  string(wf.NodeMyType),
        Label: "mytype",
        Dot:   "bg-amber-500",
        Hint:  "what it does",
    }
}
func (m *module) Render() registry.NodeRender {
    return registry.NodeRender{Head: "mytype", Hint: "GET / POST", CSSType: "mytype", Inputs: 1, Outputs: 1}
}

// DrawflowDataFromYAML projects wf.Node typed fields into the inner
// data blob the inspector reads. Only emit keys with non-zero values so
// the YAML round-trip stays tidy (omitempty doesn't help — drawflow
// stores raw map). __arg_modes is special: preserve it whenever set
// so the Fixed/Expression toggle round-trips through publish.
func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
    data := map[string]any{"url": n.URL, "method": n.Method}
    if n.Body != "" { data["body"] = n.Body }
    if len(n.ArgModes) > 0 { data["__arg_modes"] = n.ArgModes }
    return data
}

// YAMLFromDrawflowData is the inverse — read inspector state into a
// wf.Node. Map fields collected by the kvlist widget come back as
// []any (JSON-decoded), coerce with a helper.
func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
    n := wf.Node{ID: id, Type: wf.NodeMyType}
    n.URL, _ = inner["url"].(string)
    n.Method, _ = inner["method"].(string)
    n.Body, _ = inner["body"].(string)
    n.ArgModes = stringMap(inner["__arg_modes"])
    return n
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }
func (m *module) InspectorScript() string           { return "mytype/inspector.js" }
```

### `inspector.templ` — partial rendered into the inspector modal

The thinnest possible partial: one `wf-inspector-panel` block with `data-node-type` matching the slug, and one `ArgForm` call that reflects the schema. The editor.js dispatcher shows/hides the panel by matching `data-node-type`.

```go
package mytype

import (
    "github.com/yogasw/wick/internal/entity"
    engnodes "github.com/yogasw/wick/internal/agents/workflow/nodes"
    wfview "github.com/yogasw/wick/internal/tools/agents/view/workflow"
)

templ Inspector() {
    <div class="wf-inspector-panel hidden" data-node-type="mytype">
        <div id="ins-mytype-args" class="space-y-2">
            @wfview.ArgForm(entity.StructToConfigs(engnodes.MyTypeSchema{}))
        </div>
    </div>
}
```

**Why ArgForm:** it iterates the reflected `entity.Config` rows and emits one `.wf-arg-field` wrapper per field, complete with:
- label, required asterisk, description text
- the widget matching the tag (`textarea` / `dropdown=a|b|c` / `kvlist=name|value` / `picker=<src>` / `number` / `secret` / …)
- Fixed | Expression toggle pill (default Fixed)
- live template preview slot (filled in Expression mode against the INPUT pane)
- drop target for INPUT pane JSON leaves (auto-flips to Expression)
- `data-cfg-visible-when` for conditional fields (`visible_when=method:POST|PUT|PATCH|DELETE`)

### `inspector.js` — hydrate/save glue

```js
(function () {
  'use strict';
  function container() { return document.getElementById('ins-mytype-args'); }

  const mod = {
    meta: { kind: 'mytype', head: 'mytype', hint: 'GET / POST', cssType: 'mytype', inputs: 1, outputs: 1, defaults: { method: 'GET' } },
    onDrop(data) { if (!data.method) data.method = 'GET'; },

    hydrate(inner) {
      const helpers = window.wickEditorHelpers;
      const c = container();
      if (!helpers || !c) return;
      if (!c.dataset.hydrated) {
        helpers.hydrateArgsForm(c, c.innerHTML, buildArgsFromInner(inner), inner.__arg_modes || {}, '');
        c.dataset.hydrated = '1';
      } else {
        // Subsequent opens — restore values + mode without re-injecting HTML
        // (keeps focus + scroll position). Walk wf-arg-fields manually.
      }
    },

    save(inner) {
      const helpers = window.wickEditorHelpers;
      const args = helpers.collectArgs(container());
      const modes = helpers.collectArgModes(container());
      inner.url = args.url || '';
      inner.method = args.method || 'GET';
      // Headers/Query kvlist → map[string]string. kvJSONToMap drops blanks.
      inner.headers = kvJSONToMap(args.headers || '');
      // Track per-field arg_modes for the engine to honor at runtime.
      const trimmed = {};
      for (const k of Object.keys(modes)) if (modes[k] === 'expression') trimmed[k] = 'expression';
      if (Object.keys(trimmed).length > 0) inner.__arg_modes = trimmed;
      else delete inner.__arg_modes;
    },

    attach({ requestUpdate }) {
      // editor.js delegates input/change on document.body for .wf-arg-field —
      // no extra wiring needed. Use attach only for custom widgets (regen
      // buttons, mode toggles inside the partial, etc.).
      void requestUpdate;
    },
  };
  window.WickNodes = window.WickNodes || {};
  window.WickNodes.mytype = mod;
})();
```

The shared helpers exposed on `window.wickEditorHelpers` (defined in [`internal/tools/agents/js/workflow/editor.js`](../../../internal/tools/agents/js/workflow/editor.js)):

| Helper | What it does |
|---|---|
| `hydrateArgsForm(container, html, args, modes, lookupModule)` | Re-injects HTML, wires Fixed/Expression toggles, repaints kvlist + picker rows, attaches drop targets, fires `wireVisibleWhen` |
| `collectArgs(container)` | Walks every `.wf-arg-field` and returns `{[key]: value}` (kvlist hidden serializes to JSON array; picker hidden serializes to chip JSON) |
| `collectArgModes(container)` | Returns `{[key]: "fixed"\|"expression"}` from wrappers' `dataset.argMode` |
| `setArgFieldMode(wrap, mode, persist)` | Programmatically flip the toggle |

### `all/all.go` + `static.go`

Register the new subpackage with a blank import so `init()` fires at boot, and extend the embed glob so the inspector JS is reachable at `/static/nodes/<type>/inspector.js`:

```go
// internal/tools/agents/workflow/nodes/all/all.go
import (
    _ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/http"
    _ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/mytype"
    _ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/session_init"
)
```

```go
// internal/tools/agents/workflow/nodes/static.go
//go:embed all:http all:mytype all:session_init
var StaticFS embed.FS
```

## Fixed vs Expression — runtime contract

`ArgForm` gives every single-string field a `Fixed | Expression` toggle. The toggle state is persisted as `n.ArgModes[<key>]` (one of `"fixed"` or `"expression"`, default `"fixed"` for new fields when nothing is recorded).

The executor MUST honor `ArgModes` for any field that supports it. Use a helper:

```go
func renderField(n workflow.Node, key, raw string, rctx workflow.RenderCtx) (string, error) {
    if mode, ok := n.ArgModes[key]; ok && mode == "fixed" {
        return raw, nil
    }
    return template.Render(raw, rctx)
}
```

Map fields (`kvlist=name|value` → `map[string]string` like Headers / Query) are typically always-expression — each value is rendered as a template. Don't expose a per-row toggle (cell-by-cell mode is awkward UX); document that values can use templates and tell users to escape literal `{{` with `{{` + `"{{x}}"` + `}}` if they really need it.

## Contract

### `Executor` interface (`workflow/executor.go`)

```go
type Executor interface {
    Execute(ctx context.Context, node Node, rctx *RunContext) (NodeOutput, error)
}
```

### `Describer` interface (`engine/engine.go`)

```go
type Describer interface {
    Descriptor() engine.NodeDescriptor
}
```

Implement on the executor struct (pointer receiver). Optional but **strongly recommended** — without it the node appears in workflows but never surfaces in `workflow_node_types` so AI cannot discover its schema.

### `NodeDescriptor` fields

| Field | Purpose |
|---|---|
| `Description` | One-liner shown in palette + AI catalog. Action verbs, describe input/output shape. |
| `WhenToUse` | Disambiguation — when to pick this node over the closest alternative |
| `Example` | YAML snippet copy-pasteable into workflow.yaml. Use real field values, not placeholders |
| `Schema` | `integration.StructSchema(myOpSchema{})` — never hardcode the map |
| `Output` | `map[string]string` field name → description. Becomes `{{.Node.<id>.<key>}}` reference in templates |
| `Docs` (embedded `wickdocs.Docs`) | Opt-in self-documentation: `Quirks`, `Examples`, `InputSample`, `OutputSample`, `TemplateableFields`, `PairWith`, `CommonPitfalls`. Surfaced by `workflow_node_detail`. Zero-value = no extra context. See [pkg/wickdocs/docs.go](../../../pkg/wickdocs/docs.go) and design [doc 24](../../../internal/docs/workflow/24-describe-contract.md). |

### Optional declarer interfaces (`engine/declarers.go`)

When your node has runtime semantics that `workflow_describe` / `workflow_validate` can't infer from the common pool, implement one or both of these optional interfaces on the executor:

```go
// engine.DependencyDeclarer — surface external surfaces the node touches.
type DependencyDeclarer interface {
    Dependencies(n workflow.Node) []engine.NodeDependency
}

// engine.TemplateableFieldsDeclarer — extend the cross-ref scan.
type TemplateableFieldsDeclarer interface {
    TemplateableFields(n workflow.Node) map[string]string
}
```

`workflow_describe` calls these per node when present, falling back to a generic switch (channel / connector / agent / classify) and a fixed field pool (`prompt`, `prompt_file`, `url`, `body`, `expr`, `input`, `expression`, `sql`) otherwise. **No registration needed** — implementing the interface on your executor is the wiring.

Implement `Dependencies` when the node touches an external surface a future maintainer would want to find via impact search ("which workflows break if we retire sheet ABC?"). Canonical kinds:

| `engine.DepKind*` | Use for |
|---|---|
| `Channel` | Inbound/outbound channel nodes — emit `<channel>.<op>` for outbound, channel name for triggers |
| `Connector` | Connector op invocation — emit `<module>.<op>` |
| `Provider` | LLM provider — `claude` / `codex` / `gemini` |
| `Dataset` | Dataset binding |
| `Env` / `Secret` | When the node consumes a named env / secret key |
| `Webhook` | Outbound webhook |
| `Sheet` / `HTTP` / `File` | Self-explanatory |
| custom string | Anything else — appears under `deps.other.<kind>` |

Implement `TemplateableFields` when your schema carries Go-template strings on fields outside the default pool. Each returned entry is `path → value`; `path` is the label used in `workflow_describe` issue paths (e.g. `args.channel`, `headers.Authorization`, `sheet.range`).

**Canonical examples to read:**

- `nodes/channel.go::Dependencies` + `TemplateableFields` — declares `<channel>.<op>` and exposes per-arg templates.
- `nodes/connector.go` — same pattern for connector ops.
- `nodes/http.go` — declares the URL as an HTTP dep and exposes Headers + Query map values for the template scan.
- `nodes/agent.go` / `nodes/classify.go` — declares the provider as a `provider` dep.

### Schema struct tags

Same `wick:"..."` grammar as Tools / Connectors / Channel events. See the **config-tags** skill (sibling folder) for the full grid. Common modifiers for node schemas:

| Tag | Effect |
|---|---|
| `required` | Field must be present |
| `key=name` | Override the snake_cased field name |
| `desc=...` | Help text — surfaces in inspector + AI schema |
| `textarea` | Multi-line input widget — pair with `visible_when` to hide irrelevant bodies |
| `dropdown=a\|b\|c` | Enum constraint |
| `number` | Numeric input (auto-applied for `int` / `float` Go fields too) |
| `secret` | Masked input; value never sent to browser as plaintext |
| `kvlist=col1\|col2` | Editable row table — use `kvlist=name\|value` for map-shaped fields like Headers / Query. Stores as JSON `[{name,value},...]`; on save the inspector glue converts to `map[string]string` |
| `picker=<source>` | Lookup-backed typeahead (rare for nodes; common for channel match) |
| `visible_when=field:a\|b\|c` | Conditional row — hide unless dependency field equals one of the pipe-separated values. Use for method-gated bodies (`visible_when=method:POST\|PUT\|PATCH\|DELETE`) |

### `NodeOutput` shape

```go
type NodeOutput struct {
    Verdict    string         // routing key for classify/branch
    Confidence float64
    Reasoning  string
    Result     any
    Fields     map[string]any // merged into top-level when exposed as {{.Node.<id>.X}}
}
```

For most nodes use `Fields` for typed outputs:

```go
return workflow.NodeOutput{Fields: map[string]any{
    "status": resp.StatusCode,
    "body":   string(raw),
}}, nil
```

Match the keys you put in `Fields` to the `Output` map in `Descriptor()` — that's the contract AI relies on.

## Template rendering

Args that bear user-supplied expressions (URL, body, header values, command, etc.) MUST be rendered via `template.Render` or `template.RenderInto`:

```go
import "github.com/yogasw/wick/internal/agents/workflow/template"

rctx := rc.RenderCtx()
url, err := template.Render(n.URL, rctx)
```

For nodes that accept a free-form `Args map[string]any` (like channel / connector), use `renderArgsWithModes(n.Args, n.ArgModes, rc)` — that helper honors per-field `fixed` vs `expression` mode from the inspector.

Available built-in template functions live in `template/template.go::BuiltinFuncs` — auto-exposed to AI via `workflow_workspace.format_contracts.template_functions`. To add a new function: add to both `BuiltinFuncs` (impl) and `BuiltinFuncDocs` (name + description) — they're paired, single source of truth.

## Golden rules for `Execute`

1. **MUST** honor `ctx`. Any network call uses `http.NewRequestWithContext(ctx, …)`. Any blocking op selects on `<-ctx.Done()`. Skip = goroutine leak on workflow cancel.
2. **MUST** validate required fields on `n` early — return error before any I/O.
3. **MUST** render template-bearing strings before use. Raw `{{.Event.Payload.x}}` substrings in URL / body = bug.
4. **MUST** populate `Fields` keys that match the `Output` map you advertised in `Descriptor()`. Renaming a field is a breaking change for every downstream `{{.Node.<id>.X}}` reference.
5. **SHOULD** wrap upstream / dependency errors with `fmt.Errorf("…: %w", err)` so the error chain renders cleanly in run history.
6. **SHOULD** use `provider.Registry`, `connector.Registry`, etc. injected via constructor — never global singletons.
7. **MAY** emit progress logging via `rc` if your op is long-running.

## Anti-patterns

- ❌ Hardcoding schema in `mcp/mcp.go::NodeTypesCatalog` — `Descriptor()` is the only source.
- ❌ `http.NewRequest` without context — goroutine leak on workflow cancel.
- ❌ Field name in `Fields` map ≠ key in `Output` doc — AI writes broken `{{.Node.X.Y}}` based on doc, runtime returns nothing.
- ❌ Reading `n.<Field>` for a field that isn't declared in the schema struct — works at runtime but invisible to AI/inspector.
- ❌ Skipping `eng.Register` in `setup/manager.go` — engine returns "no executor for type X" at first run.
- ❌ Putting the schema struct in a different package (`mcp/`, `types.go`) — defeats the purpose of co-location.
- ❌ Mutating state across `Execute` calls on the same executor — engine reuses one executor instance for every concurrent run.

## Special node types

| Type | Notes |
|---|---|
| **One executor, many node types** | Like `DatasetExecutor` — provide `<Name>Descriptor(t workflow.NodeType) engine.NodeDescriptor` switch helper. Use `RegisterWithDesc` per type. |
| **Branching nodes** | Return `Verdict` (string) — engine filters outgoing edges by matching `case:` label. See `BranchExecutor`. |
| **Classify nodes** | Same `Verdict` mechanism; provider integration via injected `provider.Registry`. |
| **End nodes** | Terminator. Set `Result` field — surfaces as `{{.Run.final_result}}`. |
| **Parallel / merge** | No new schema fields needed — engine reads `Branches` / `Inputs` / `Strategy` directly off `Node`. Descriptor doc should explain the fan-out/fan-in shape. |

## MCP discovery surface

AI clients reach your node via the workflow connector. Five ops to know:

| Op | What it does | Powered by |
|---|---|---|
| `workflow_node_types` | List of all node types with summary + schema | `engine.Descriptors` (from your `Descriptor()`) |
| `workflow_node_detail(node_type)` | Full self-doc for ONE type: schema, output, quirks, examples, samples, pair_with, pitfalls | `Descriptor()` + embedded `wickdocs.Docs` |
| `workflow_template_test(template, sample_event?\|context?)` | Render a Go template against a synthetic context. Missing-key errors return `available_keys` + did-you-mean hint | `template.Render` + context introspection |
| `workflow_picker_resolve(source, query?, limit?)` | Resolve picker source (`slack.channels`, `slack.users`, `slack.usergroups`) to `[{id, name}]` | `mcp.PickerRegistry` — channels register backends at setup |
| `workflow_validate(id)` | Parse + validate with did-you-mean / hint pointers on common error shapes | `parse.Validate` + `mcp.ValidateRich` |
| `workflow_describe(id)` | Human summary: triggers, graph shape, deps, dangling-edge + template-ref warnings | walks workflow + honors declarer interfaces above |
| `workflow_get_run_log(id, run_id, diagnose=true?)` | Default: status + per-node duration. With `diagnose=true`: classify error into one of 8 known classes (template_missing_key / channel_action_missing / connector_op_missing / secret_leak / branch_no_edge / agent_session_invalid / provider_skill_missing / unknown), surface `available_keys` + a `suggested_fix` with confidence level | `mcp.diagnose.go` rule registry |
| `workflow_watch({workflow_id?, trigger_id?, node_id?, status?, since?, limit?, wait_seconds?, expect?, stop_on_first?})` | Bounded, filterable read over recent runs. Returns only `[run_id, workflow_id, status, started_at, ended_at, trigger_id]` — AI follows up with `workflow_get_run_log(diagnose=true)` per chosen id. `wait_seconds>0` subscribes to the live event stream and returns the moment `expect`/`stop_on_first` is met. Server caps `limit=50` + `wait_seconds=30`. | `mcp.watch.go` + `engine.broker.go` |

**Watch usage patterns** (see [doc 25](../../../internal/docs/workflow/25-diagnose-watch.md)):

- **Integration test, one trigger:** `wait_seconds=15, stop_on_first=true` — AI is blocked until run lands, returns in ~1s when user triggers.
- **Test N triggers in one shot:** `wait_seconds=20, expect=2` — returns after the 2nd run, not after 20s.
- **Negative case (filter should reject):** call after the negative trigger — `runs=[]` is the confirmation that the filter held.
- **Prod debug, large history:** `status=failed, since=-1h, limit=20` — peek mode (wait=0), returns immediately; AI samples 1–2 IDs and diagnoses, never bulk-loads.

**Why watch is cheap:** the body is loaded ONLY when AI explicitly picks a run via `workflow_get_run_log`. Watch itself reads the sharded index in constant time. Long-poll subscribes to the engine's multi-subscriber broker (drops on slow consumers; never back-pressures the engine).

**Diagnose flag — what AI gets:** structured `error_class` + `suggested_fix { node_id, field, current, suggested, confidence, rationale }`. Per the no-auto-fix rule, AI surfaces the suggestion to the user and applies via existing `workflow_update_node` / `workflow_write_file` only after confirmation. Confidence levels:

- `high` — Levenshtein ≤ 1 from the typo (e.g. `channel` → `channelx`) or exact case-fold match. Safe to suggest with high certainty.
- `medium` — distance 2 (e.g. `sednmessage` → `send_message`). Likely-but-not-certain.
- `low` — distance ≥ 3 or no good candidate. Surface as a hint only.

**Extending the classifier:** drop a new entry into `errorRules` in
`internal/agents/workflow/mcp/diagnose.go`. Each entry is a (regex, handler)
pair; handlers receive a `DiagnoseCtx` carrying the run, the workflow, and
the registries needed to suggest a fix. First matching rule wins — keep
specific patterns before general ones.

**Contract guarantees for the declarers:**

- `wickdocs.Docs.InputSample` / `OutputSample` — JSON string (not Go literal). Surfaced as-is by `workflow_node_detail` so the editor can render "try it" panels and AI sees realistic shapes.
- `wickdocs.Docs.Examples` — full YAML node blocks copy-pasteable into `workflow.yaml`. Use them for usage patterns; reserve InputSample/OutputSample for request/response shapes.
- `Dependencies` return value is JSON-serialised verbatim — pick stable `Ref` strings; refactors that change them break impact-analysis queries.

## Verifying your work

```bash
go build ./internal/...
```

Smoke from MCP:

1. Boot wick — `go run main.go server &`
2. Call `workflow_node_types` — verify your new entry appears with the schema you declared.
3. Call `workflow_node_detail("<your_type>")` — confirm `quirks`, `examples`, `input_sample`, `output_sample` show what you populated (omitted when empty; that's intentional).
4. Create a draft workflow that uses the node, call `workflow_validate` — confirms no schema errors and that any did-you-mean hints surface on typos.
5. Call `workflow_describe(id)` — confirm your `Dependencies` declarer (if any) surfaces the right `Ref`, and `TemplateableFields` (if any) catches templates pointing at undeclared nodes.
6. Call `workflow_simulate` with a synthetic event — confirms `Execute` runs and outputs match your `Output` doc.
7. For templating bugs use `workflow_template_test` with `sample_event` instead of round-tripping through write_file → simulate.
8. Kill the port.

## When to ask before acting

- **New node type vs new arg on existing node** — confirm with user. Adding a field is almost always cheaper.
- **Removing a node type** — orphans every workflow.yaml that references it. Migration plan needs to land same change.
- **Renaming output fields** — breaks every `{{.Node.<id>.X}}` reference in user workflows. Treat as breaking change.
- **New template function** — confirm name + signature with user; functions are global to every workflow.

## Reference

**Engine side**

- Canonical example: [`nodes/http.go`](../../../internal/agents/workflow/nodes/http.go) — exported `HTTPSchema`, `Descriptor()`, `ArgModes` honouring via `renderHTTPField`
- Multi-type executor: [`nodes/dataset.go`](../../../internal/agents/workflow/nodes/dataset.go) — `DatasetDescriptor(t)` switch
- Engine + Descriptor types: [`engine/engine.go`](../../../internal/agents/workflow/engine/engine.go)
- Executor interface: [`executor.go`](../../../internal/agents/workflow/executor.go)
- Node struct + NodeType constants: [`types.go`](../../../internal/agents/workflow/types.go)
- Wiring site: [`setup/manager.go`](../../../internal/agents/workflow/setup/manager.go)
- MCP catalog builder: [`mcp/mcp.go::NodeTypesCatalog`](../../../internal/agents/workflow/mcp/mcp.go)
- Template engine: [`template/template.go`](../../../internal/agents/workflow/template/template.go)
- Tag grammar: sibling `config-tags` skill folder

**Editor UI side**

- Canonical example: [`tools/agents/workflow/nodes/http/`](../../../internal/tools/agents/workflow/nodes/http/) — ArgForm + kvlist + Fixed/Expression toggle + visible_when
- Simpler example (hand-coded inputs): [`tools/agents/workflow/nodes/session_init/`](../../../internal/tools/agents/workflow/nodes/session_init/)
- Registry: [`tools/agents/workflow/nodes/registry.go`](../../../internal/tools/agents/workflow/nodes/registry.go)
- Aggregator blank-imports: [`tools/agents/workflow/nodes/all/all.go`](../../../internal/tools/agents/workflow/nodes/all/all.go)
- Embed glob: [`tools/agents/workflow/nodes/static.go`](../../../internal/tools/agents/workflow/nodes/static.go)
- ArgForm + chrome: [`tools/agents/view/workflow/argform.templ`](../../../internal/tools/agents/view/workflow/argform.templ)
- Shared editor helpers: [`tools/agents/js/workflow/editor.js`](../../../internal/tools/agents/js/workflow/editor.js) — `window.wickEditorHelpers`
- Widget pool: [`internal/manager/view/type/`](../../../internal/manager/view/type/) — `fieldtype.Text` / `Textarea` / `Dropdown` / `KVList` / `Picker` / `Secret` / `Number` / …
