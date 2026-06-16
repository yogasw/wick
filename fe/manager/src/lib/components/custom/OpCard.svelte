<script lang="ts">
  /* One operation card: identity (key/name/destructive), LLM description,
     per-call inputs (FieldRow CRUD), and — for HTTP-backed ops — the
     request recipe (method, URL template, header rows, body template,
     content type). MCP-backed ops show a read-only source chip and no
     request block. Mirrors the legacy custom_review.js opCard(). */
  import { TextInput, TextArea, Select, KvList } from "@wick-fe/common-ui";
  import FieldRow from "./FieldRow.svelte";
  import { METHODS, newField } from "./draft.js";
  import type { DraftOp } from "$lib/types.js";

  type Props = {
    op: DraftOp;
    onChange: () => void;
    onRemove: () => void;
  };
  let { op, onChange, onRemove }: Props = $props();

  function set<K extends keyof DraftOp>(k: K, v: DraftOp[K]) {
    op[k] = v;
    onChange();
  }

  function addInput() {
    op.inputs = [...op.inputs, newField()];
    onChange();
  }

  function removeInput(i: number) {
    op.inputs = op.inputs.filter((_, idx) => idx !== i);
    onChange();
  }

  /* Header rows ride the KvList contract (array of {key,value}); the Draft
     stores them as an object, so map both ways on every edit. */
  let headerRows = $derived(
    Object.entries(op.request?.headers ?? {}).map(([key, value]) => ({ key, value })),
  );

  function setHeaders(rows: Record<string, string>[]) {
    if (!op.request) return;
    const next: Record<string, string> = {};
    for (const r of rows) {
      next[r.key ?? ""] = r.value ?? "";
    }
    op.request.headers = next;
    onChange();
  }

  function setReq<K extends keyof NonNullable<DraftOp["request"]>>(
    k: K,
    v: NonNullable<DraftOp["request"]>[K],
  ) {
    if (!op.request) return;
    op.request[k] = v;
    onChange();
  }
</script>

<div class="space-y-3 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4">
  <div class="flex items-start gap-2">
    <div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
      <div class="max-w-[10rem]">
        <TextInput value={op.key} onChange={(v) => set("key", v)} placeholder="op_key" ariaLabel="Operation key" class="font-mono" />
      </div>
      <div class="max-w-[14rem]">
        <TextInput value={op.name} onChange={(v) => set("name", v)} placeholder="Display name" ariaLabel="Operation name" />
      </div>
      <label class="flex items-center gap-1 whitespace-nowrap text-[11px] text-black-800 dark:text-black-600">
        <input type="checkbox" class="accent-green-500" checked={op.destructive} onchange={(e) => set("destructive", (e.target as HTMLInputElement).checked)} aria-label="Destructive" />
        destructive
      </label>
      {#if op.mcp_source}
        <span class="inline-flex items-center rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600">MCP · {op.mcp_source.tool_name}</span>
      {/if}
    </div>
    <button
      type="button"
      class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600"
      title="Delete operation"
      aria-label="Delete operation"
      onclick={onRemove}
    >
      <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" stroke-linecap="round" stroke-linejoin="round"/><path d="M10 11v6M14 11v6" stroke-linecap="round"/></svg>
    </button>
  </div>

  <TextInput value={op.description} onChange={(v) => set("description", v)} placeholder="Description shown to the LLM — action verbs, be specific." ariaLabel="Operation description" />

  <div class="flex items-center justify-between">
    <span class="text-[11px] font-semibold uppercase tracking-wider text-black-800 dark:text-black-600">Inputs (per-call, LLM provides)</span>
    <button type="button" class="rounded-lg border border-white-400 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600" onclick={addInput}>+ Add input</button>
  </div>
  <div class="space-y-2">
    {#if op.inputs.length === 0}
      <p class="text-xs text-black-700 dark:text-black-600">No fields yet.</p>
    {/if}
    {#each op.inputs as input, i (i)}
      <FieldRow field={input} {onChange} onRemove={() => removeInput(i)} />
    {/each}
  </div>

  {#if op.request}
    <div class="text-[11px] font-semibold uppercase tracking-wider text-black-800 dark:text-black-600">Request</div>
    <div class="grid grid-cols-12 gap-2">
      <div class="col-span-3">
        <Select value={(op.request.method || "GET").toUpperCase()} options={METHODS} onChange={(v) => setReq("method", v)} />
      </div>
      <div class="col-span-9">
        <TextInput value={op.request.url_template} onChange={(v) => setReq("url_template", v)} placeholder={"{{.cfg.base_url}}/path"} ariaLabel="URL template" class="font-mono" />
      </div>
    </div>

    <KvList
      columns={["key", "value"]}
      rows={headerRows}
      onChange={setHeaders}
      label="Headers (values are templates)"
      addLabel="+ Add header"
      placeholders={{ key: "Header-Name", value: "Bearer {{.cfg.auth_value}}" }}
    />

    <div>
      <span class="block text-[11px] text-black-800 dark:text-black-600">Body template (Go text/template — {"{{.cfg.*}}"} / {"{{.in.*}}"}; funcs: default, lower, upper, b64, urlquery, js, printf)</span>
      <TextArea value={op.request.body_template} onChange={(v) => setReq("body_template", v)} rows={3} ariaLabel="Body template" class="mt-1" />
    </div>
    <TextInput value={op.request.content_type} onChange={(v) => setReq("content_type", v)} placeholder="content type, e.g. application/json" ariaLabel="Content type" class="font-mono" />
  {/if}
</div>
