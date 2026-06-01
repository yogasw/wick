<script lang="ts">
  // Per-op datatable inspector — table picker + conditional builders:
  //   get        → primary key (single value)
  //   exists / query / count / delete  → conditions list
  //   query      → + order_by list + limit + offset
  //   insert / upsert  → row fields list
  //
  // Field shape mirrors the Go side at
  // internal/agents/workflow/types.go (Node.Conditions / OrderBy / Key
  // / RowValues / Table / Limit / Offset). Each templatable value
  // pairs with a Fixed ⇄ Expression mode pill stored in
  // node.condition_modes[column] / node.row_modes[column].

  import type { Node, DataTableCond, DataTableOrder } from "$lib/types/workflow";
  import { draftWorkflow, updateNode } from "$lib/stores/editor";
  import { workflowAPI } from "$lib/api/workflow";
  import ArgField from "../fields/ArgField.svelte";
  import Field from "../fields/Field.svelte";

  type Props = { node: Node };
  let { node }: Props = $props();

  // Available data table aliases declared at the workflow root —
  // `workflow.data_tables[].alias` is the key the engine resolves.
  // Drop the typo path by offering a select instead of free text.
  const tableAliases = $derived(
    ($draftWorkflow?.data_tables ?? []).map((t) => t.alias),
  );

  // Resolve the picked alias → backend table slug → column list, used
  // to populate every per-row column dropdown. The workflow's
  // `data_tables[]` carries `alias` (operator's nickname) and `table`
  // (real slug); fall back to alias when `table` is omitted so older
  // workflows still autocomplete. Cached in-memory across renders.
  const tableSlug = $derived.by<string>(() => {
    const t = node.table;
    if (!t) return "";
    const binding = ($draftWorkflow?.data_tables ?? []).find((b) => b.alias === t);
    return binding?.table ?? t;
  });
  let columnsCache = $state<Record<string, string[]>>({});
  let columnsLoading = $state(false);
  $effect(() => {
    const slug = tableSlug;
    if (!slug || columnsCache[slug]) return;
    columnsLoading = true;
    void workflowAPI
      .dataTableColumns(slug)
      .then((res) => {
        columnsCache = { ...columnsCache, [slug]: (res ?? []).map((c) => c.name) };
      })
      .catch((e) => console.warn("data-table columns fetch failed:", e))
      .finally(() => (columnsLoading = false));
  });
  const columnNames = $derived(tableSlug ? columnsCache[tableSlug] ?? [] : []);

  const op = $derived(node.type.replace(/^datatable_/, ""));

  const showConditions = $derived(
    op === "exists" || op === "query" || op === "count" || op === "delete",
  );
  const showOrderLimit = $derived(op === "query");
  const showRows = $derived(op === "insert" || op === "upsert");
  // Key builder appears for get + insert + upsert. Insert/upsert need
  // both `key` (target row identifier) AND `row` (column values to
  // write); the Go schema marks both as required.
  const showKey = $derived(op === "get" || op === "insert" || op === "upsert");

  function patch(field: keyof Node, value: unknown) {
    updateNode(node.id, { [field]: value } as Partial<Node>);
  }

  // ── Conditions ────────────────────────────────────────────────────
  const OPS = [
    "equals",
    "not_equals",
    "gt",
    "gte",
    "lt",
    "lte",
    "contains",
    "in",
    "is_empty",
    "is_not_empty",
  ];

  function addCondition() {
    patch("conditions", [
      ...(node.conditions ?? []),
      { column: "", op: "equals", value: "" } as DataTableCond,
    ]);
  }

  function updateCondition(i: number, patchCond: Partial<DataTableCond>) {
    const next = [...(node.conditions ?? [])];
    next[i] = { ...next[i], ...patchCond };
    patch("conditions", next);
  }

  function removeCondition(i: number) {
    const next = [...(node.conditions ?? [])];
    next.splice(i, 1);
    patch("conditions", next);
  }

  function condMode(column: string): "fixed" | "expression" {
    const m = node.condition_modes?.[column];
    return m === "expression" ? "expression" : "fixed";
  }

  function setCondMode(column: string, mode: "fixed" | "expression") {
    const next = { ...(node.condition_modes ?? {}) };
    next[column] = mode;
    patch("condition_modes", next);
  }

  // ── Order by ──────────────────────────────────────────────────────
  function addOrder() {
    patch("order_by", [
      ...(node.order_by ?? []),
      { column: "", direction: "asc" } as DataTableOrder,
    ]);
  }

  function updateOrder(i: number, patchOrd: Partial<DataTableOrder>) {
    const next = [...(node.order_by ?? [])];
    next[i] = { ...next[i], ...patchOrd };
    patch("order_by", next);
  }

  function removeOrder(i: number) {
    const next = [...(node.order_by ?? [])];
    next.splice(i, 1);
    patch("order_by", next);
  }

  // ── Row fields (insert / upsert) ──────────────────────────────────
  function setRowValue(column: string, value: string) {
    const next = { ...(node.row ?? {}) };
    next[column] = value;
    patch("row", next);
  }

  function renameRowColumn(oldKey: string, newKey: string) {
    if (!newKey || newKey === oldKey) return;
    const next: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(node.row ?? {})) {
      next[k === oldKey ? newKey : k] = v;
    }
    patch("row", next);
    if (node.row_modes) {
      const m: Record<string, string> = {};
      for (const [k, v] of Object.entries(node.row_modes)) {
        m[k === oldKey ? newKey : k] = v;
      }
      patch("row_modes", m);
    }
  }

  function removeRow(column: string) {
    const next = { ...(node.row ?? {}) };
    delete next[column];
    patch("row", next);
    if (node.row_modes) {
      const m = { ...node.row_modes };
      delete m[column];
      patch("row_modes", m);
    }
  }

  let newRowColumn = $state("");
  let newRowValue = $state("");
  function addRow() {
    const c = newRowColumn.trim();
    if (!c) return;
    setRowValue(c, newRowValue);
    newRowColumn = "";
    newRowValue = "";
  }

  function rowMode(column: string): "fixed" | "expression" {
    const m = node.row_modes?.[column];
    return m === "expression" ? "expression" : "fixed";
  }

  function setRowMode(column: string, mode: "fixed" | "expression") {
    const next = { ...(node.row_modes ?? {}) };
    next[column] = mode;
    patch("row_modes", next);
  }

  // ── Primary key (get) ─────────────────────────────────────────────
  // node.key is a map column → value. For the common single-PK case
  // the legacy editor surfaces just one input writing into key.id.
  // Show all entries here so composite PKs are addressable, plus an
  // implicit "id" fast path when nothing is set yet.
  function setKey(column: string, value: string) {
    const next = { ...(node.key ?? {}) };
    next[column] = value;
    patch("key", next);
  }

  const keyEntries = $derived(Object.entries(node.key ?? { id: "" }));
</script>

<!-- Table picker — every datatable_* op needs this. Aliases come from
     the workflow root `data_tables[].alias` so adding a new binding
     surfaces here automatically; falls back to free text when no
     bindings are declared yet so first-time setup still works. -->
{#if tableAliases.length > 0}
  <Field
    kind="select"
    label="Data table"
    value={node.table ?? ""}
    onChange={(v) => patch("table", v)}
    options={[
      { label: "(select alias)", value: "" },
      ...tableAliases.map((a) => ({ label: a, value: a })),
    ]}
    helper="Pick from workflow.data_tables[] aliases."
  />
{:else}
  <Field
    kind="text"
    label="Data table"
    value={node.table ?? ""}
    onChange={(v) => patch("table", v)}
    placeholder="alias defined in workflow.data_tables[]"
    helper="No data_tables declared yet — define one in the workflow YAML first; this dropdown will populate."
  />
{/if}

{#if showKey}
  <!-- ── get: primary key ───────────────────────────────────────── -->
  <div class="space-y-2">
    <div class="flex items-center justify-between">
      <span class="text-xs font-medium">Primary key</span>
      <button
        type="button"
        class="text-emerald-600 text-xs"
        onclick={() => setKey(`col${keyEntries.length}`, "")}
        title="Add another column (composite PK)"
      >+ add column</button>
    </div>
    {#each keyEntries as [column, value]}
      <div class="rounded border border-slate-200 dark:border-slate-700 p-2 space-y-1">
        <div class="flex items-center gap-2">
          <input
            class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
            list="dt-columns-{node.id}"
            placeholder="column name"
            value={column}
            onchange={(e) => {
              const next: Record<string, unknown> = {};
              for (const [k, v] of Object.entries(node.key ?? {})) {
                next[k === column ? (e.target as HTMLInputElement).value : k] = v;
              }
              patch("key", next);
            }}
          />
          <button
            type="button"
            class="text-rose-500 text-xs px-2"
            onclick={() => {
              const next = { ...(node.key ?? {}) };
              delete next[column];
              patch("key", next);
            }}
          >✕</button>
        </div>
        <ArgField
          label="value"
          value={typeof value === "string" ? value : JSON.stringify(value)}
          mode={condMode(column)}
          placeholder={"{{.Event.Payload.id}}"}
          onValueChange={(v) => setKey(column, v)}
          onModeChange={(m) => setCondMode(column, m)}
        />
      </div>
    {/each}
  </div>
{/if}

{#if showConditions}
  <!-- ── exists / query / count / delete: condition rows ────────── -->
  <div class="space-y-2">
    <div class="flex items-center justify-between">
      <span class="text-xs font-medium">Filter conditions</span>
      <button
        type="button"
        class="text-emerald-600 text-xs"
        onclick={addCondition}
      >+ add condition</button>
    </div>
    <span class="text-[11px] text-slate-500 dark:text-slate-400">
      Ops: equals / not_equals / gt / gte / lt / lte / contains / in / is_empty
      / is_not_empty
    </span>
    {#each node.conditions ?? [] as cond, i (i)}
      <div class="rounded border border-slate-200 dark:border-slate-700 p-2 space-y-1">
        <div class="flex items-center gap-2">
          <input
            class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
            list="dt-columns-{node.id}"
            placeholder={columnNames.length > 0 ? "column" : (columnsLoading ? "loading columns…" : "column")}
            value={cond.column}
            oninput={(e) =>
              updateCondition(i, { column: (e.target as HTMLInputElement).value })}
          />
          <select
            class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 text-[12px]"
            value={cond.op}
            onchange={(e) => updateCondition(i, { op: (e.target as HTMLSelectElement).value })}
          >
            {#each OPS as o}
              <option value={o}>{o}</option>
            {/each}
          </select>
          <button
            type="button"
            class="text-rose-500 text-xs px-2"
            onclick={() => removeCondition(i)}
          >✕</button>
        </div>
        {#if cond.op !== "is_empty" && cond.op !== "is_not_empty"}
          <ArgField
            label="value"
            value={typeof cond.value === "string" ? cond.value : JSON.stringify(cond.value ?? "")}
            mode={condMode(cond.column)}
            placeholder="…"
            onValueChange={(v) => updateCondition(i, { value: v })}
            onModeChange={(m) => setCondMode(cond.column, m)}
          />
        {/if}
      </div>
    {/each}
  </div>
{/if}

{#if showOrderLimit}
  <!-- ── query: order_by + limit + offset ───────────────────────── -->
  <div class="space-y-2">
    <div class="flex items-center justify-between">
      <span class="text-xs font-medium">Order by</span>
      <button
        type="button"
        class="text-emerald-600 text-xs"
        onclick={addOrder}
      >+ add</button>
    </div>
    {#each node.order_by ?? [] as ord, i (i)}
      <div class="flex items-center gap-2">
        <input
          class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
          list="dt-columns-{node.id}"
          placeholder="column"
          value={ord.column}
          oninput={(e) => updateOrder(i, { column: (e.target as HTMLInputElement).value })}
        />
        <select
          class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 text-[12px]"
          value={ord.direction ?? "asc"}
          onchange={(e) => updateOrder(i, { direction: (e.target as HTMLSelectElement).value })}
        >
          <option value="asc">asc</option>
          <option value="desc">desc</option>
        </select>
        <button
          type="button"
          class="text-rose-500 text-xs px-2"
          onclick={() => removeOrder(i)}
        >✕</button>
      </div>
    {/each}
  </div>
  <div class="grid grid-cols-2 gap-2">
    <label class="flex flex-col gap-1">
      <span class="text-xs font-medium">Limit</span>
      <input
        type="number"
        class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
        placeholder="25"
        value={node.limit ?? 0}
        oninput={(e) => patch("limit", Number((e.target as HTMLInputElement).value) || 0)}
      />
    </label>
    <label class="flex flex-col gap-1">
      <span class="text-xs font-medium">Offset</span>
      <input
        type="number"
        class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
        placeholder="0"
        value={node.offset ?? 0}
        oninput={(e) => patch("offset", Number((e.target as HTMLInputElement).value) || 0)}
      />
    </label>
  </div>
{/if}

{#if showRows}
  <!-- ── insert / upsert: row fields ────────────────────────────── -->
  <div class="space-y-2">
    <div class="flex items-center justify-between">
      <span class="text-xs font-medium">Fields</span>
    </div>
    <span class="text-[11px] text-slate-500 dark:text-slate-400">
      id / created_at / updated_at are auto-managed by the engine.
    </span>
    {#each Object.entries(node.row ?? {}) as [column, value] (column)}
      <div class="rounded border border-slate-200 dark:border-slate-700 p-2 space-y-1">
        <div class="flex items-center gap-2">
          <input
            class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
            list="dt-columns-{node.id}"
            value={column}
            onchange={(e) => renameRowColumn(column, (e.target as HTMLInputElement).value)}
          />
          <button
            type="button"
            class="text-rose-500 text-xs px-2"
            onclick={() => removeRow(column)}
          >✕</button>
        </div>
        <ArgField
          label="value"
          value={typeof value === "string" ? value : JSON.stringify(value)}
          mode={rowMode(column)}
          placeholder="…"
          onValueChange={(v) => setRowValue(column, v)}
          onModeChange={(m) => setRowMode(column, m)}
        />
      </div>
    {/each}
    <div class="flex items-center gap-2 pt-1 border-t border-slate-200 dark:border-slate-700">
      <input
        class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
        list="dt-columns-{node.id}"
        placeholder="column"
        bind:value={newRowColumn}
        onkeydown={(e) => e.key === "Enter" && addRow()}
      />
      <input
        class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
        placeholder="value"
        bind:value={newRowValue}
        onkeydown={(e) => e.key === "Enter" && addRow()}
      />
      <button
        type="button"
        class="text-emerald-600 text-xs px-2"
        onclick={addRow}
      >+</button>
    </div>
  </div>
{/if}

<!-- Shared column-name autocomplete source. Every column input
     references this via `list="dt-columns-<node.id>"`; populated
     lazily from /api/data-tables/<slug>/columns on first table
     selection so the operator gets typeahead instead of typo'ing. -->
<datalist id="dt-columns-{node.id}">
  {#each columnNames as col}
    <option value={col}></option>
  {/each}
</datalist>
