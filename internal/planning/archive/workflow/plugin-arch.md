# Per-node plugin architecture

Status: **implemented** (2026-05-16). Pattern intro buat session_init;
existing nodes (classify/agent/shell/http/dll) masih di legacy switch
table, migrate satu-satu sesuai kebutuhan.
Update terakhir: 2026-05-16.

---

## TODO

- [ ] Migrate `classify` node ke plugin folder (untuk validate pattern dgn complex node)
- [ ] Migrate `agent` node ke plugin folder (session_from override pindah dari editor.js)
- [ ] Migrate `branch`/`http`/`shell`/`channel`/`connector` (deferred until concrete need)
- [ ] Add codec test that round-trips per-node YAML ↔ Drawflow via registry path
- [ ] CLI scaffolder — `wick workflow new-node <type>` generate boilerplate (folder + 3 files)
- [ ] Claude Code skill — `node-builder` yg AI bisa pakai (lihat [skill-node-builder.md](skill-node-builder.md))

---

## Motivasi

Tiap node baru dulu sentuh 6+ file:

| File | Yg diubah |
|---|---|
| `agents/workflow/types.go` | Tambah field per-type di Node struct |
| `agents/workflow/parse/parse.go` | Validator case |
| `agents/workflow/nodes/<type>.go` | Executor implementation |
| `agents/workflow/setup/manager.go` | Register executor |
| `tools/agents/view/workflow/models.go` | Palette entry |
| `tools/agents/workflows_codec.go` | Drawflow ↔ YAML switch |
| `tools/agents/view/workflow/editor_inspector.templ` | Inspector HTML panel |
| `tools/agents/js/workflow/editor.js` | JS hydrate / save / onDrop hooks |

Problem: kalau cuma butuh 1 node baru (atau remove node yg ngak dipake),
nyentuh banyak file, conflict prone, hard to grep.

Plugin arch isolates UI/codec concern ke 1 folder. Existing nodes
boleh tetep di legacy switch — migration optional.

---

## Architecture

```
internal/tools/agents/workflow/nodes/
├── registry.go              # Module interface + Register/All/ByType
├── static.go                # embed.FS untuk per-node static assets
├── all/all.go               # blank-import aggregator (semua module load disini)
└── <type>/                  # 1 folder = 1 node type
    ├── meta.go              # Module impl (palette, render, codec)
    ├── inspector.templ      # Partial templ untuk inspector parameters tab
    ├── inspector_templ.go   # generated
    └── inspector.js         # vanilla JS module — onDrop / hydrate / save / attach
```

**Domain side** (`internal/agents/workflow/`) tetep flat union — Node
struct + parser + executor. Tools side (UI/canvas) yg plugin-ize.
Alasan: YAML decode butuh single struct (Go type system), tapi UI
purely composition.

---

## Module interface (Go)

```go
// internal/tools/agents/workflow/nodes/registry.go
type Module interface {
    NodeType() wf.NodeType
    PaletteSection() string
    PaletteItem() PaletteItem
    Render() NodeRender
    DrawflowDataFromYAML(n wf.Node) map[string]any
    YAMLFromDrawflowData(id string, inner map[string]any) wf.Node
    InspectorPartial() templ.Component
    InspectorScript() string  // path under /static/nodes/<type>/
}

func Register(m Module)
func All() []Module
func ByType(t wf.NodeType) Module
```

Each module:

```go
// internal/tools/agents/workflow/nodes/<type>/meta.go
package <type>

func init() { registry.Register(&module{}) }

type module struct{}

func (m *module) NodeType() wf.NodeType                          { return wf.Node<Type> }
func (m *module) PaletteSection() string                         { return "AI" }
func (m *module) PaletteItem() registry.PaletteItem              { /* ... */ }
func (m *module) Render() registry.NodeRender                    { /* ... */ }
func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any  { /* ... */ }
func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node { /* ... */ }
func (m *module) InspectorPartial() templ.Component              { return Inspector() }
func (m *module) InspectorScript() string                        { return "<type>/inspector.js" }
```

Blank-import di `all/all.go`:

```go
import (
    _ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/<type>"
)
```

Server `handler.go` sudah blank-import `nodes/all` — drop folder, ngak
sentuh handler.

---

## Inspector partial (templ)

```go
// internal/tools/agents/workflow/nodes/<type>/inspector.templ
package <type>

templ Inspector() {
    <div class="wf-inspector-panel hidden" data-node-type="<type>">
        <!-- form controls — DOM IDs ins-<type>-* convention -->
    </div>
}
```

editor.js show/hide:

```js
function showModulePanelFor(kind) {
    document.querySelectorAll('.wf-inspector-panel').forEach((el) => {
        el.classList.toggle('hidden', el.dataset.nodeType !== kind);
    });
}
```

---

## JS module

```js
// internal/tools/agents/workflow/nodes/<type>/inspector.js
(function () {
    'use strict';

    const mod = {
        meta: {
            kind: '<type>',
            head: '<type>',
            hint: 'hint',
            cssType: '<type>',
            inputs: 1,
            outputs: 1,
            defaults: { /* seed fields */ },
        },
        onDrop(data)         { /* set defaults */ },
        hydrate(inner)       { /* DOM from inner */ },
        save(inner)          { /* inner from DOM */ },
        attach({ requestUpdate }) {
            // wire DOM listeners (regen button, mode toggle, etc)
        },
    };

    window.WickNodes = window.WickNodes || {};
    window.WickNodes['<type>'] = mod;
})();
```

editor.js dispatch:

```js
const mod = window.WickNodes[kind];
mod?.hydrate?.(inner);  // on inspector open
mod?.save?.(inner);     // on field change
mod?.onDrop?.(data);    // on palette drag-drop
mod?.attach?.({ ... }); // once at boot
```

---

## Static serving

`static.go` di `nodes/` embed semua subfolder:

```go
//go:embed all:<type>
var StaticFS embed.FS
```

`handler.go`:

```go
r.Static("/static/nodes/", wfnodes.StaticFS)
```

`editor.templ` emit script tag per module:

```go
templ NodeModuleScripts(base string) {
    for _, m := range wfnodes.All() {
        if path := m.InspectorScript(); path != "" {
            <script src={ base + "/static/nodes/" + path } defer></script>
        }
    }
}
```

Drop folder → embed pickup otomatis → script tag emit otomatis.

---

## Composition di palette + codec

`view/workflow/models.go BuildPalette` loop modules → append items ke
section yg match `PaletteSection()`:

```go
for _, m := range wfnodes.All() {
    item := PaletteItem{...}
    for i := range sections {
        if sections[i].Title == m.PaletteSection() {
            sections[i].Items = append(sections[i].Items, item)
            break
        }
    }
}
```

`workflows_codec.go renderFor` + `nodeDataFromWorkflow` +
`workflowNodeFromDrawflow` — switch fallback ke registry:

```go
default:
    if mod := wfnodes.ByType(t); mod != nil {
        return mod.Render()  // or DrawflowDataFromYAML / YAMLFromDrawflowData
    }
```

Legacy nodes (classify/agent/dll) tetep di switch case sebelum default.
Migrate satu-satu = pindahin case ke module folder.

---

## Adding a new node

```bash
mkdir internal/tools/agents/workflow/nodes/<type>
# tulis 3 file: meta.go, inspector.templ, inspector.js
echo "_ \"github.com/yogasw/wick/internal/tools/agents/workflow/nodes/<type>\"" >> internal/tools/agents/workflow/nodes/all/all.go
templ generate -path internal/tools/agents/workflow/nodes/<type>/inspector.templ
go build ./internal/...
```

Plus engine side (still flat union):
1. Add `NodeType` const di `internal/agents/workflow/types.go`
2. Add per-type field ke `Node` struct (kalau perlu)
3. Add validator case di `parse/parse.go validateNodeBody`
4. Add executor di `internal/agents/workflow/nodes/<type>.go`
5. Register di `internal/agents/workflow/setup/manager.go`

5 step engine + 3 step UI = total ~8 file untuk node baru. Lebih
banyak dari "1 folder = 1 node" ideal, tapi engine side wajib karena
type-safe YAML decode + central executor registry.

**Future:** schema-driven approach — module declare YAML schema
(reflection), validator + executor auto-wire. Defer sampai 3+ node
baru pakai pattern ini (avoid premature abstraction).

---

## Reference module

[`internal/tools/agents/workflow/nodes/session_init/`](../../tools/agents/workflow/nodes/session_init/)
— full working impl. Copy ini sebagai template buat node baru.
