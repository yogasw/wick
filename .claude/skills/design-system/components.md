# Component Patterns

Patterns for the most common UI components. Each section shows the
recommended implementation in Go templ + Tailwind, followed by common
mistakes to avoid. All examples use tokens from `tokens.md`.

---

## 1. Button

Three variants, three sizes.

### Variants

| Variant | Style |
|---|---|
| **Primary** | `bg-green-500 text-white-100 hover:bg-green-600` |
| **Secondary** | `border border-green-500 text-green-500 hover:bg-green-200` |
| **Destructive** | `border border-error-400 text-error-400 hover:bg-error-100` |

### Sizes (padding)

| Size | Padding | Tailwind |
|---|---|---|
| Small | 8px | `px-2 py-2` |
| Regular | 16px | `px-4 py-4` |
| Large | 24px | `px-6 py-6` |

### Disabled state (all variants)

`bg-white-300 dark:bg-navy-600 text-black-700 cursor-not-allowed`

### Templ example — primary button

```go
<button
    type="button"
    class="inline-flex items-center justify-center rounded-lg px-6 py-4
           bg-green-500 text-white-100 font-medium
           hover:bg-green-600 active:bg-green-700
           focus:outline-none focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800
           disabled:bg-white-300 disabled:text-black-700 disabled:cursor-not-allowed
           transition-colors"
>
    Save
</button>
```

### Common mistakes

```go
// WRONG: using green for a "success" confirmation button
<button class="bg-green-500 ...">Saved!</button>
// RIGHT: use pos-* for success feedback
<div class="bg-pos-100 text-pos-400 p-4 rounded">Saved successfully</div>

// WRONG: off-grid padding
<button class="px-3 py-[10px] ...">Next</button>
// RIGHT: use documented sizes (8/16/24)
<button class="px-4 py-4 ...">Next</button>
```

---

## 2. Text input

### Anatomy

- **Label** above the field: `text-sm font-medium text-black-900 dark:text-white-100`
- **Input box**: 16px padding, border, full-width
- **Placeholder**: `text-black-700 dark:text-black-600`

### States

| State | Border | Background |
|---|---|---|
| Default | `border-white-400 dark:border-navy-500` | `bg-white-100 dark:bg-navy-700` |
| Hover | `border-black-700` | same |
| Focus | `border-green-500 ring-2 ring-green-200 dark:ring-green-800` | same |
| Disabled | `border-white-300 dark:border-navy-600` | `bg-white-200 dark:bg-navy-800` |
| Error | `border-error-400` | `bg-error-100` |

### Templ example

```go
<label class="block">
    <span class="block mb-2 text-sm font-medium text-black-900 dark:text-white-100">
        Label
    </span>
    <input
        type="text"
        placeholder="Enter value..."
        class="block w-full px-4 py-4 rounded-lg
               bg-white-100 dark:bg-navy-700
               text-black-900 dark:text-white-100
               placeholder:text-black-700 dark:placeholder:text-black-600
               border border-white-400 dark:border-navy-500
               hover:border-black-700
               focus:border-green-500 focus:ring-2 focus:ring-green-200
               dark:focus:ring-green-800 focus:outline-none
               disabled:bg-white-200 dark:disabled:bg-navy-800
               disabled:text-black-700"
    />
</label>
```

### Common mistakes

```go
// WRONG: missing dark mode pairing
<input class="bg-white-100 text-black-900 border-white-400" />
// RIGHT: always pair
<input class="bg-white-100 dark:bg-navy-700 text-black-900 dark:text-white-100
              border-white-400 dark:border-navy-500" />

// WRONG: raw hex color
<input style="border-color: #27B199;" />
// RIGHT: use token
<input class="focus:border-green-500" />
```

---

## 3. Select dropdown

Same styling as text input, plus the custom arrow.

```go
<label class="block">
    <span class="block mb-2 text-sm font-medium text-black-900 dark:text-white-100">
        Category
    </span>
    <select
        class="block w-full px-4 py-4 rounded-lg appearance-none select-arrow
               bg-white-100 dark:bg-navy-700
               text-black-900 dark:text-white-100
               border border-white-400 dark:border-navy-500
               focus:border-green-500 focus:ring-2 focus:ring-green-200
               dark:focus:ring-green-800 focus:outline-none"
    >
        <option value="">Select...</option>
    </select>
</label>
```

The `.select-arrow` class is defined in `web/src/input.css` and adds a
chevron SVG as background image.

---

## 4. Card / Surface

- Background: `bg-white-100 dark:bg-navy-700`
- Border: `border border-white-300 dark:border-navy-600`
- Padding: `p-6` (24px) or `p-8` (32px)
- Shadow: `shadow-md` for info cards, `shadow-lg` for modals

### Templ example

```go
<div class="rounded-xl border border-white-300 dark:border-navy-600
            bg-white-100 dark:bg-navy-700 p-8 shadow-md">
    <h2 class="text-lg font-semibold text-black-900 dark:text-white-100">
        Card Title
    </h2>
    <p class="mt-2 text-sm text-black-800 dark:text-black-600">
        Card description text.
    </p>
</div>
```

### Common mistakes

```go
// WRONG: raw Tailwind gray (not in our palette)
<div class="bg-gray-50 border-gray-200">...</div>
// RIGHT: use project tokens
<div class="bg-white-100 dark:bg-navy-700 border-white-300 dark:border-navy-600">...</div>

// WRONG: invented shadow
<div style="box-shadow: 0 4px 11px 2px rgba(13,42,99,0.27);">...</div>
// RIGHT: use shadow tier token
<div class="shadow-md">...</div>
```

---

## 5. Tabs

- Active: `text-green-500 border-b-2 border-green-500`
- Inactive: `text-black-800 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100`
- Divider: `border-b border-white-300 dark:border-navy-600`

```go
<div role="tablist" class="flex gap-8 border-b border-white-300 dark:border-navy-600">
    <button
        role="tab"
        aria-selected="true"
        class="pb-3 -mb-px text-sm font-semibold
               text-green-500 border-b-2 border-green-500"
    >
        Active Tab
    </button>
    <button
        role="tab"
        aria-selected="false"
        class="pb-3 -mb-px text-sm font-medium
               text-black-800 dark:text-black-600
               hover:text-black-900 dark:hover:text-white-100"
    >
        Inactive Tab
    </button>
</div>
```

---

## 6. Modal / Dialog

- Backdrop: semi-transparent overlay, `fixed inset-0 z-50`
- Panel: `bg-white-100 dark:bg-navy-700`, `p-6` or `p-8`, `shadow-lg`
- Actions: right-aligned, secondary (cancel) + primary (confirm), `gap-4`

```go
<!-- Backdrop -->
<div class="fixed inset-0 z-50 flex items-center justify-center bg-black-900/50">
    <!-- Panel -->
    <div class="bg-white-100 dark:bg-navy-700 rounded-xl p-8 shadow-lg
                w-full max-w-md mx-4">
        <h2 class="text-lg font-semibold text-black-900 dark:text-white-100 mb-4">
            Confirm Action
        </h2>
        <p class="text-sm text-black-800 dark:text-black-600 mb-6">
            Are you sure you want to proceed?
        </p>
        <div class="flex justify-end gap-4">
            <button class="px-4 py-4 rounded-lg border border-green-500
                           text-green-500 font-medium hover:bg-green-200
                           transition-colors">
                Cancel
            </button>
            <button class="px-4 py-4 rounded-lg bg-green-500 text-white-100
                           font-medium hover:bg-green-600 transition-colors">
                Confirm
            </button>
        </div>
    </div>
</div>
```

---

## 7. Status badges

Use the appropriate status ramp — never green for success.

| Tone | Background | Text |
|---|---|---|
| Success | `bg-pos-100 text-pos-400` | |
| Info | `bg-prog-100 text-prog-400` | |
| Warning | `bg-cau-100 text-cau-400` | |
| Negative | `bg-neg-100 text-neg-400` | |
| Error | `bg-error-100 text-error-800` | |
| Warning (alt) | `bg-warning-100 text-warning-800` | |
| Link | `bg-link-100 text-link-800` | |

```go
<span class="inline-flex items-center px-3 py-1 rounded-full
             text-xs font-medium bg-pos-100 text-pos-400">
    Active
</span>

<span class="inline-flex items-center px-3 py-1 rounded-full
             text-xs font-medium bg-neg-100 text-neg-400">
    Failed
</span>
```

### Common mistakes

```go
// WRONG: green for "success"
<span class="bg-green-500 text-white-100">Active</span>
// RIGHT: use pos-* ramp
<span class="bg-pos-100 text-pos-400">Active</span>

// WRONG: navy for "info"
<span class="bg-navy-500 text-white-100">Pending</span>
// RIGHT: use prog-* ramp
<span class="bg-prog-100 text-prog-400">Pending</span>
```

---

## 8. Notification / Alert card

- Padding: `p-4` (16px)
- Leading colored dot for status
- Title: `font-semibold text-black-900 dark:text-white-100`
- Metadata: `text-xs text-black-800 dark:text-black-600`

```go
<div class="flex items-start gap-3 p-4 rounded-lg
            bg-white-100 dark:bg-navy-700
            border border-white-300 dark:border-navy-600">
    <!-- Status dot -->
    <span class="mt-1.5 h-2 w-2 rounded-full bg-pos-400 shrink-0"></span>
    <div class="min-w-0 flex-1">
        <p class="text-sm font-semibold text-black-900 dark:text-white-100">
            Notification title
        </p>
        <p class="mt-1 text-xs text-black-800 dark:text-black-600">
            #123 — 17 Apr 2026
        </p>
    </div>
    <a href="#" class="text-sm text-link-400 hover:underline shrink-0">
        View
    </a>
</div>
```

---

## 9. Search field

Text input with a leading search icon.

```go
<div class="relative">
    <span class="pointer-events-none absolute inset-y-0 left-4 flex items-center">
        <svg class="h-5 w-5 text-black-700 dark:text-black-600" viewBox="0 0 24 24"
             fill="none" stroke="currentColor" stroke-width="2"
             stroke-linecap="round" stroke-linejoin="round">
            <circle cx="11" cy="11" r="8"></circle>
            <line x1="21" y1="21" x2="16.65" y2="16.65"></line>
        </svg>
    </span>
    <input
        type="search"
        placeholder="Search..."
        class="block w-full pl-12 pr-4 py-4 rounded-full
               bg-white-100 dark:bg-navy-700
               text-black-900 dark:text-white-100
               placeholder:text-black-700 dark:placeholder:text-black-600
               border border-white-300 dark:border-navy-600
               focus:border-green-500 focus:ring-2 focus:ring-green-200
               dark:focus:ring-green-800 focus:outline-none"
    />
</div>
```

---

## 10. Page layout (tool page template)

Standard layout for any tool page. Uses the shared `@ui.Layout` and
`@ui.Navbar` components — never write raw `<html>` or a custom nav.

```go
templ IndexPage(user *entity.User) {
    @ui.Layout("Tool Name") {
        @ui.Navbar(user)
        <main class="mx-auto w-full max-w-container px-6 py-8">
            <h1 class="text-[1.75rem] font-semibold leading-tight tracking-tight
                        text-black-900 dark:text-white-100">
                Tool Name
            </h1>
            <p class="mt-2 text-sm text-black-800 dark:text-black-600">
                Short description of what this tool does.
            </p>

            <!-- Tool content: cards, forms, etc. -->
            <div class="mt-6 grid gap-6 md:grid-cols-2">
                <!-- responsive grid example -->
            </div>
        </main>
        <script src="/tools/mytool/static/js/mytool.js"></script>
    }
}
```

### Responsive notes

- Container is `max-w-container` (1120px) with `px-6` gutters.
- Use `grid gap-6 md:grid-cols-2` for two-column layouts that stack
  on mobile.
- Test at ≤375px — single column, no horizontal scroll.
- Buttons should be `w-full sm:w-auto` on mobile if side-by-side
  doesn't fit.

---

## 11. General do's and don'ts

### Colors — always use tokens

```go
// WRONG: raw hex
<div style="background: #27B199;">...</div>
// RIGHT: named token
<div class="bg-green-500">...</div>

// WRONG: Tailwind default gray (not in our palette)
<div class="bg-gray-50 text-gray-700">...</div>
// RIGHT: project tokens
<div class="bg-white-200 dark:bg-navy-800 text-black-800 dark:text-black-600">...</div>
```

### Spacing — stay on grid

```go
// WRONG: arbitrary value
<div class="p-[13px] gap-[7px]">...</div>
// RIGHT: 8-pixel scale
<div class="p-4 gap-2">...</div>
```

### Typography — Inter, three weights

```go
// WRONG: foreign font or invalid weight
<h1 class="font-bold">...</h1>       <!-- font-bold = 700, not in system -->
// RIGHT: allowed weights only
<h1 class="font-semibold">...</h1>   <!-- 600 -->
```

### Body text — Black ramp only

```go
// WRONG: accent color on paragraph text
<p class="text-green-500">Lorem ipsum...</p>
// RIGHT: black ramp
<p class="text-black-900 dark:text-white-100">Lorem ipsum...</p>
<p class="text-black-800 dark:text-black-600">Muted subtitle.</p>
```

### Dark mode — always pair

```go
// WRONG: light only
<div class="bg-white-100 text-black-900 border-white-300">...</div>
// RIGHT: paired
<div class="bg-white-100 dark:bg-navy-700 text-black-900 dark:text-white-100
            border-white-300 dark:border-navy-600">...</div>
```

---

## 12. KVList (editable table config field)

Used in the admin config block form for `kvlist`-type fields. Renders an
always-visible mini-table with inline inputs, add-row, and remove-row buttons.
Auto-saves via debounced `input` listeners (no submit button needed).

### Tag (in Config struct)

```go
QuestionGroups string `wick:"kvlist=id|name|label;desc=Row definitions."`
// bare kvlist defaults to a single "value" column:
Mapping string `wick:"kvlist;desc=Simple list."`
```

### Value format

Stored as JSON in the `configs.value` column:

```json
[{"id":"1","name":"Sales","label":"Q1"},{"id":"2","name":"Support","label":"Q2"}]
```

Read in Go:

```go
var rows []map[string]string
json.Unmarshal([]byte(c.Cfg("question_groups")), &rows)
```

### Rendered structure (from `kvlistBlock` + `kvDataRow` in configs.templ)

```
┌─ Block card (rounded-xl border) ─────────────────────────┐
│ Header: key · col1 · col2    [✓ saved]                    │
│ Description (if any)                                       │
├───────────────────────────────────────────────────────────┤
│  col1 │ col2 │ col3  │ ×                                   │  ← header row
│ ──────┼──────┼───────┼────                                │
│ [inp] │[inp] │ [inp] │ ×                                   │  ← data rows
│ ...                                                        │
├───────────────────────────────────────────────────────────┤
│  [+ Add Row]  (dashed button, full width)                  │
└───────────────────────────────────────────────────────────┘
```

### Notes

- Column names come from `Options` (pipe-separated), set by the `kvlist=` tag flag.
- Auto-save: `input` events on any `[data-col]` input debounce 800ms then POST JSON to the config endpoint. Row removal triggers immediate save.
- `data-save-status` span in the header shows `saving…` / `✓ saved` / `✗ failed`.
- `data-missing-badge` clears client-side on first successful save with non-empty rows.
- JS helpers `kvBlockAddRow` / `kvBlockRemoveRow` are exposed on `window` by `configsSaveScript()`.

### Common mistakes

```go
// WRONG: using kvlist for a single flat list — use textarea or dropdown instead
Notes string `wick:"kvlist;desc=Freeform notes."`
// RIGHT: kvlist makes sense when data has 2+ columns OR when rows are structured
Endpoints string `wick:"kvlist=name|url|method;desc=API endpoints."`

// WRONG: reading kvlist value as a plain string
val := c.Cfg("endpoints")  // val is raw JSON — useless as a plain string

// RIGHT: unmarshal the JSON array
var rows []map[string]string
_ = json.Unmarshal([]byte(c.Cfg("endpoints")), &rows)
```
