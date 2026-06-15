<script lang="ts">
  /* Postman-style operation runner for one connector row. Mirrors the
     legacy connector_test.templ + connector_test.js: an op dropdown whose
     selection is URL-synced via ?op= (replaceState, no navigation), an
     input form driven by the op's input schema, a Run button, and a result
     panel showing status / latency_ms / response or error. Execution POSTs
     through the JSON test endpoint; the op input schema comes from
     /test-meta. Reuses common-ui primitives + toasts via common-stores. */
  import { Button, Select, TextInput, NumberInput, TextArea } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import { getTestMeta, runConnectorTest } from "$lib/api.js";
  import type { TestMeta, TestOp, TestInputField, TestRunResult } from "$lib/types.js";

  type Props = { connectorKey: string; connectorId: string };
  let { connectorKey, connectorId }: Props = $props();

  let meta = $state<TestMeta | null>(null);
  let loading = $state(true);
  let error = $state("");
  let activeOp = $state("");
  let accountId = $state("");
  let inputs = $state<Record<string, string>>({});
  let running = $state(false);
  let result = $state<TestRunResult | null>(null);

  let ops = $derived(meta?.ops ?? []);
  let accounts = $derived(meta?.accounts ?? []);
  let current = $derived(ops.find((o) => o.key === activeOp) ?? null);

  function opFromUrl(): string {
    try {
      return new URLSearchParams(window.location.search).get("op") ?? "";
    } catch {
      return "";
    }
  }

  function syncOpToUrl(opKey: string): void {
    const next = `${window.location.pathname}?op=${encodeURIComponent(opKey)}`;
    if (window.location.pathname + window.location.search !== next) {
      history.replaceState({}, "", next);
    }
  }

  function resetInputs(op: TestOp | null): void {
    const next: Record<string, string> = {};
    for (const f of op?.input ?? []) {
      next[f.key] = f.type === "checkbox" ? "false" : "";
    }
    inputs = next;
  }

  function selectOp(opKey: string): void {
    activeOp = opKey;
    result = null;
    resetInputs(current);
    syncOpToUrl(opKey);
  }

  function setInput(key: string, value: string): void {
    inputs = { ...inputs, [key]: value };
  }

  async function load(): Promise<void> {
    loading = true;
    error = "";
    try {
      meta = await getTestMeta(connectorKey, connectorId);
      const list = meta.ops ?? [];
      const fromUrl = opFromUrl();
      const initial = list.find((o) => o.key === fromUrl) ? fromUrl : list[0]?.key ?? "";
      activeOp = initial;
      resetInputs(list.find((o) => o.key === initial) ?? null);
      if (initial) syncOpToUrl(initial);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function run(): Promise<void> {
    if (running || !activeOp) return;
    running = true;
    result = null;
    try {
      result = await runConnectorTest(connectorKey, connectorId, activeOp, inputs, accountId);
    } catch (e) {
      toastError("Run failed", e instanceof Error ? e.message : String(e));
      result = { operation: activeOp, status: "error", error: e instanceof Error ? e.message : String(e) };
    } finally {
      running = false;
    }
  }

  function statusKind(status: string | undefined): "running" | "success" | "error" {
    if (status === "success") return "success";
    if (status === "error") return "error";
    return "running";
  }

  function fieldType(f: TestInputField): "text" | "textarea" | "checkbox" | "number" {
    if (f.type === "textarea") return "textarea";
    if (f.type === "checkbox" || f.type === "bool" || f.type === "boolean") return "checkbox";
    if (f.type === "number") return "number";
    return "text";
  }

  function responseText(res: TestRunResult): string {
    if (res.error) return res.error;
    return JSON.stringify(res.response ?? null, null, 2);
  }

  $effect(() => { load(); });

  const statusClasses: Record<string, string> = {
    running: "bg-prog-100 text-prog-400",
    success: "bg-pos-100 text-pos-400",
    error: "bg-neg-100 text-neg-400",
  };
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if meta}
  <div class="space-y-6">
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Test runner</h1>
        <p class="mt-1 text-sm text-black-800 dark:text-black-600">Pick an operation, fill the input, and run it against this row's credentials. Calls are recorded as <code class="font-mono text-xs">source=test</code> in the run log.</p>
      </div>
      <Button variant="secondary" size="md" onclick={() => push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(connectorId)}/history`)}>View history</Button>
    </div>

    {#if ops.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-8 text-center">
        <p class="text-sm text-black-700 dark:text-black-600">This connector exposes no operations.</p>
      </div>
    {:else}
      <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="flex flex-col gap-2 sm:flex-row sm:items-end">
          <div class="flex-1">
            <label for="test-op" class="block text-xs font-medium text-black-800 dark:text-black-600">Operation</label>
            <div class="mt-1">
              <Select value={activeOp} options={ops.map((o) => ({ label: o.name, value: o.key }))} onChange={selectOp} />
            </div>
          </div>
          {#if accounts.length > 0}
            <div class="sm:w-48">
              <label for="test-account" class="block text-xs font-medium text-black-800 dark:text-black-600">Run as</label>
              <div class="mt-1">
                <Select value={accountId} options={[{ label: "Default credentials", value: "" }, ...accounts.map((a) => ({ label: `@${a.display_name}`, value: a.id }))]} onChange={(v) => (accountId = v)} />
              </div>
            </div>
          {/if}
          <Button size="lg" disabled={running} onclick={run}>{running ? "Running…" : "Run"}</Button>
        </div>

        {#if current}
          <div class="mt-4 space-y-3">
            {#if (current.input ?? []).length === 0}
              <p class="text-xs text-black-700 dark:text-black-600">This operation takes no input.</p>
            {/if}
            {#each current.input ?? [] as field (field.key)}
              <div>
                <label for={`test-input-${field.key}`} class="block text-xs font-medium text-black-800 dark:text-black-600">
                  {field.key}
                  {#if field.required}<span class="text-neg-400">*</span>{/if}
                </label>
                {#if field.description}
                  <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">{field.description}</p>
                {/if}
                <div class="mt-1">
                  {#if fieldType(field) === "textarea"}
                    <TextArea value={inputs[field.key] ?? ""} rows={4} ariaLabel={field.key} onChange={(v) => setInput(field.key, v)} />
                  {:else if fieldType(field) === "checkbox"}
                    <label class="inline-flex items-center gap-3 cursor-pointer select-none">
                      <input id={`test-input-${field.key}`} type="checkbox" class="w-4 h-4 accent-green-500 cursor-pointer rounded" checked={inputs[field.key] === "true"} onchange={(e) => setInput(field.key, (e.target as HTMLInputElement).checked ? "true" : "false")} />
                      <span class="text-xs text-black-800 dark:text-black-600">{inputs[field.key] === "true" ? "true" : "false"}</span>
                    </label>
                  {:else if fieldType(field) === "number"}
                    <NumberInput value={Number(inputs[field.key]) || 0} ariaLabel={field.key} onChange={(n) => setInput(field.key, String(n))} />
                  {:else}
                    <TextInput value={inputs[field.key] ?? ""} ariaLabel={field.key} onChange={(v) => setInput(field.key, v)} />
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        {/if}

        {#if result}
          <div class="mt-4">
            <div class="flex items-center gap-2 text-xs text-black-700 dark:text-black-600">
              <span class="rounded-full px-2 py-0.5 font-medium {statusClasses[statusKind(result.status)]}">{result.status ?? "running"}</span>
              {#if typeof result.latency_ms === "number"}<span>{result.latency_ms} ms</span>{/if}
            </div>
            <pre class="mt-2 max-h-96 overflow-auto rounded-lg bg-white-200 dark:bg-navy-800 p-3 font-mono text-xs text-black-900 dark:text-white-100">{responseText(result)}</pre>
          </div>
        {/if}
      </section>
    {/if}
  </div>
{/if}
