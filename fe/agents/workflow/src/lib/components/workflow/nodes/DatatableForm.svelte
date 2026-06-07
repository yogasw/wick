<script lang="ts">
  // Per-op datatable inspector. Matches v1's two-pane layout 1:1:
  //
  //   table       (always)  — workspace-level data table dropdown
  //   key         (get)     — single primary-key value, writes node.key.id
  //   key         (upsert)  — same single-value contract (Go schema requires it)
  //   conditions  (exists / query / count / delete)
  //   order_by    (query)
  //   limit/off   (query)
  //   row fields  (insert / upsert) — auto-populated from columns on table change
  //
  // Wire shape mirrors workflow.Node in internal/agents/workflow/types.go:
  //   Table=string, Key=map[string]any, Conditions=[]DataTableCondYAML,
  //   OrderBy=[]DataTableOrder, RowValues=map[string]any (json key `row`),
  //   Limit=int, Offset=int. ConditionModes / RowModes are FE-only blobs
  //   keyed by column name; Go preserves them through round-trip but
  //   never reads them.
  import type { Node, DataTableCond, DataTableOrder } from "$lib/types/workflow";
  import { updateNode } from "$lib/stores/editor";
  import { workflowAPI } from "$lib/api/workflow";
  import { onMount } from "svelte";
  import ArgField from "../fields/ArgField.svelte";
  import Field from "../fields/Field.svelte";
  import ColumnCombobox from "../fields/ColumnCombobox.svelte";

  type Props = { node: Node; workflowId?: string; nodeLabels?: string[]; nodeOutputs?: Record<string, Record<string, unknown>> };
  let { node, workflowId, nodeLabels = [], nodeOutputs = {} as Record<string, Record<string, unknown>> }: Props = $props();

  // ── workspace tables ───────────────────────────────────────────────
  // Same source v1 used: GET /api/data-tables returns workspace-level
  // {slug, name} rows. Workflow.data_tables[] bindings (yaml) ARE used
  // for access control but don't affect this picker — operator picks
  // the real slug, validator gates access at run time.
  let tables = $state<{ slug: string; name: string }[]>([]);
  let tablesLoaded = $state(false);
  onMount(async () => {
    try {
      tables = await workflowAPI.dataTables();
    } catch (e) {
      console.warn("data-tables fetch failed:", e);
    } finally {
      tablesLoaded = true;
    }
  });

  // ── columns autocomplete + insert/upsert auto-fill ─────────────────
  // node.table is the slug directly (no alias indirection). Columns
  // are cached per slug so flipping between query and insert doesn't
  // refetch.
  let columnsCache = $state<Record<string, { name: string; type: string }[]>>({});
  let columnsLoading = $state(false);
  const slug = $derived(node.table ?? "");
  const columns = $derived(slug ? columnsCache[slug] ?? [] : []);
  const columnNames = $derived(columns.map((c) => c.name));

  // Track the slug we've already auto-populated for so flipping back
  // to an insert/upsert node doesn't keep stomping on user edits.
  let autofilledSlug = $state<string | null>(null);

  $effect(() => {
    if (!slug || columnsCache[slug]) return;
    columnsLoading = true;
    void workflowAPI
      .dataTableColumns(slug)
      .then((res) => {
        columnsCache = { ...columnsCache, [slug]: res ?? [] };
      })
      .catch((e) => console.warn("columns fetch failed:", e))
      .finally(() => (columnsLoading = false));
  });

  // Auto-populate row fields on insert/upsert when table is picked +
  // current row is empty. Mirrors v1's loadColumnsForInsert. Won't
  // overwrite existing entries — operator's edits survive a flip
  // between insert ↔ upsert.
  $effect(() => {
    if (!showRows) return;
    if (!slug || columns.length === 0) return;
    if (autofilledSlug === slug) return;
    const existing = node.row ?? {};
    if (Object.keys(existing).length > 0) {
      autofilledSlug = slug;
      return;
    }
    const next: Record<string, unknown> = {};
    for (const c of columns) next[c.name] = "";
    updateNode(node.id, { row: next });
    autofilledSlug = slug;
  });

  // ── op routing ─────────────────────────────────────────────────────
  const op = $derived(node.type.replace(/^datatable_/, ""));
  const showConditions = $derived(
    op === "exists" || op === "query" || op === "count" || op === "delete",
  );
  const showOrderLimit = $derived(op === "query");
  const showRows = $derived(op === "insert" || op === "upsert");
  // get + upsert need a single PK value (Go schema marks both Key
  // required). insert reads PK from row[id] / auto-managed.
  const showKey = $derived(op === "get" || op === "upsert");

  function patch(field: keyof Node, value: unknown) {
    updateNode(node.id, { [field]: value } as Partial<Node>);
  }

  // ── conditions ────────────────────────────────────────────────────
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
    return node.condition_modes?.[column] === "expression" ? "expression" : "fixed";
  }
  function setCondMode(column: string, mode: "fixed" | "expression") {
    const next = { ...(node.condition_modes ?? {}) };
    next[column] = mode;
    patch("condition_modes", next);
  }

  // ── order by ──────────────────────────────────────────────────────
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

  // ── row fields (insert / upsert) ───────────────────────────────────
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
    return node.row_modes?.[column] === "expression" ? "expression" : "fixed";
  }
  function setRowMode(column: string, mode: "fixed" | "expression") {
    const next = { ...(node.row_modes ?? {}) };
    next[column] = mode;
    patch("row_modes", next);
  }

  // ── primary key (single value) ─────────────────────────────────────
  // Go's Node.Key is map[string]any but every real workflow writes a
  // single PK. v1 surfaced one input writing into node.key directly as
  // a string; v2 follows the canonical shape and writes to key.id so
  // round-trip through YAML stays clean.
  const keyValue = $derived.by<string>(() => {
    const k = node.key as Record<string, unknown> | undefined;
    if (!k) return "";
    const v = k.id ?? Object.values(k)[0];
    return typeof v === "string" ? v : v != null ? String(v) : "";
  });
  function setKey(value: string) {
    patch("key", { id: value });
  }
</script>

<!-- Table picker — workspace tables, same source v1 used. The link
     opens the workspace data-tables admin so operators can create one
     without leaving context. -->
<Field
  kind="select"
  label="Data table"
  value={node.table ?? ""}
  onChange={(v) => patch("table", v)}
  options={[
    { label: tablesLoaded ? "(select table)" : "loading…", value: "" },
    ...tables.map((t) => ({ label: t.name ? `${t.name} (${t.slug})` : t.slug, value: t.slug })),
  ]}
/>
<div class="-mt-1 text-[11px] text-black-700 dark:text-black-600">
  Tables registered in the workspace.
  <a
    href="../data-tables"
    target="_blank"
    class="text-emerald-600 dark:text-emerald-400 hover:underline"
  >Manage tables ↗</a>
</div>

{#if showKey}
  <!-- ── get / upsert: primary key (single value) ─────────────────── -->
  <ArgField
    {workflowId}
    {nodeLabels}
    {nodeOutputs}
    label="Primary key value"
    value={keyValue}
    mode={condMode("id")}
    placeholder={"{{.Event.Payload.id}}"}
    onValueChange={setKey}
    onModeChange={(m) => setCondMode("id", m)}
    helper="Go template expression for the PK column (resolved against the run context)."
  />
{/if}

{#if showConditions}
  <!-- ── exists / query / count / delete: condition rows ──────────── -->
  <div class="space-y-2">
    <div class="flex items-center justify-between">
      <span class="text-xs font-medium">Filter conditions</span>
      <button
        type="button"
        class="text-emerald-600 text-xs"
        onclick={addCondition}
      >+ add condition</button>
    </div>
    <span class="text-[11px] text-black-700 dark:text-black-600">
      Ops: equals / not_equals / gt / gte / lt / lte / contains / in / is_empty / is_not_empty
    </span>
    {#each node.conditions ?? [] as cond, i (i)}
      <div class="rounded border border-slate-200 dark:border-navy-600 p-2 space-y-1">
        <div class="grid items-center gap-2" style="grid-template-columns: 2fr 1.4fr auto;">
          <ColumnCombobox
            value={cond.column}
            columns={columnNames}
            placeholder={columnsLoading ? "loading columns…" : "column"}
            onChange={(v) => updateCondition(i, { column: v })}
          />
          <select
            class="rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 text-[12px]"
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
    {workflowId}
    {nodeLabels}
    {nodeOutputs}
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
  <!-- ── query: order_by + limit + offset ─────────────────────────── -->
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
      <div class="grid items-center gap-2" style="grid-template-columns: 1fr auto auto;">
        <ColumnCombobox
          value={ord.column}
          columns={columnNames}
          onChange={(v) => updateOrder(i, { column: v })}
        />
        <select
          class="rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 text-[12px]"
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
        class="rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5"
        placeholder="25"
        value={node.limit ?? 0}
        oninput={(e) => patch("limit", Number((e.target as HTMLInputElement).value) || 0)}
      />
    </label>
    <label class="flex flex-col gap-1">
      <span class="text-xs font-medium">Offset</span>
      <input
        type="number"
        class="rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5"
        placeholder="0"
        value={node.offset ?? 0}
        oninput={(e) => patch("offset", Number((e.target as HTMLInputElement).value) || 0)}
      />
    </label>
  </div>
{/if}

{#if showRows}
  <!-- ── insert / upsert: row fields ──────────────────────────────── -->
  <div class="space-y-2">
    <div class="flex items-center justify-between">
      <span class="text-xs font-medium">Fields</span>
      <span class="text-[11px] text-black-700 dark:text-black-500">
        {Object.keys(node.row ?? {}).length} field{Object.keys(node.row ?? {}).length === 1 ? "" : "s"}
      </span>
    </div>
    <span class="text-[11px] text-black-700 dark:text-black-600">
      Pre-filled from the table's columns. id / created_at / updated_at are auto-managed by the engine.
    </span>
    {#each Object.entries(node.row ?? {}) as [column, value] (column)}
      <div class="rounded border border-slate-200 dark:border-navy-600 p-2 space-y-1">
        <div class="grid items-center gap-2" style="grid-template-columns: 1fr auto;">
          <ColumnCombobox
            value={column}
            columns={columnNames}
            onChange={(v) => renameRowColumn(column, v)}
          />
          <button
            type="button"
            class="text-rose-500 text-xs px-2"
            onclick={() => removeRow(column)}
          >✕</button>
        </div>
        <ArgField
    {workflowId}
    {nodeLabels}
    {nodeOutputs}
          label="value"
          value={typeof value === "string" ? value : JSON.stringify(value)}
          mode={rowMode(column)}
          placeholder="…"
          onValueChange={(v) => setRowValue(column, v)}
          onModeChange={(m) => setRowMode(column, m)}
        />
      </div>
    {/each}
    <div class="grid items-center gap-2 pt-1 border-t border-slate-200 dark:border-navy-600" style="grid-template-columns: 1fr 1fr auto;">
      <ColumnCombobox
        value={newRowColumn}
        columns={columnNames}
        onChange={(v) => (newRowColumn = v)}
      />
      <input
        class="rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 font-mono text-[12px]"
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

