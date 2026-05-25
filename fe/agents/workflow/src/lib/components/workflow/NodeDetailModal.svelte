<script lang="ts">
  // n8n-style 3-column modal for editing a node. Opened by
  // double-clicking a node on the canvas (legacy editor parity).
  //
  // Columns:
  //   LEFT   — Input: data from upstream nodes (mocked "empty" until
  //                    run history wiring lands).
  //   MIDDLE — Parameters / Settings tabs + Execute step CTA.
  //   RIGHT  — Output: last recorded JSON / Schema.
  //
  // Per-type parameter forms live inline (same as the previous
  // Inspector.svelte slide-in) so the legacy "double-click to edit"
  // flow doesn't lose any field coverage.
  import { detailNodeID, draftWorkflow, updateNode } from "$lib/stores/editor";
  import type { Node } from "$lib/types/workflow";

  const node = $derived.by<Node | null>(() => {
    const id = $detailNodeID;
    if (!id || !$draftWorkflow) return null;
    return $draftWorkflow.graph?.nodes?.find((n) => n.id === id) ?? null;
  });

  let activeTab = $state<"params" | "settings">("params");

  function close() {
    detailNodeID.set(null);
  }

  function patch(field: keyof Node, value: unknown) {
    if (!node) return;
    updateNode(node.id, { [field]: value } as Partial<Node>);
  }
</script>

{#if node}
  <div
    class="fixed inset-0 z-50 bg-slate-900/70 backdrop-blur-sm"
    role="dialog"
    aria-modal="true"
    aria-label="Edit node"
    onclick={close}
    onkeydown={(e) => e.key === "Escape" && close()}
  >
    <div
      class="rounded-lg overflow-hidden bg-white dark:bg-[#0f172a]
             text-slate-900 dark:text-slate-100 shadow-2xl flex flex-col"
      style="position:absolute; left:16px; right:16px; top:32px; bottom:32px;"
      onclick={(e) => e.stopPropagation()}
      role="presentation"
    >
      <!-- Header: node id + type + close. -->
      <header class="flex items-center gap-3 px-5 py-3 border-b border-slate-200 dark:border-slate-700">
        <span class="h-2 w-2 rounded-full bg-amber-400"></span>
        <span class="text-sm font-semibold">{node.label ?? node.id}</span>
        <span class="text-xs text-slate-500 font-mono">{node.type}</span>
        <div class="flex-1"></div>
        <button class="text-slate-400 hover:text-slate-100 text-xl leading-none" onclick={close} aria-label="Close">✕</button>
      </header>

      <!-- 3-column body. -->
      <div class="flex-1 grid divide-x divide-slate-200 dark:divide-slate-800 min-h-0" style="grid-template-columns: 1fr 2fr 1fr;">
        <!-- LEFT: input. -->
        <section class="flex flex-col p-4 overflow-y-auto">
          <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2">INPUT</div>
          <div class="flex-1 flex flex-col items-center justify-center text-slate-400 text-xs gap-3">
            <div class="text-2xl">↓</div>
            <div>No input data</div>
            <button class="px-3 py-1.5 rounded bg-rose-500 hover:bg-rose-600 text-white text-xs font-medium">Execute previous nodes</button>
            <div class="text-[11px]">to view input data</div>
          </div>
        </section>

        <!-- MIDDLE: parameters. -->
        <section class="flex flex-col overflow-y-auto">
          <nav class="flex items-center border-b border-slate-200 dark:border-slate-700 px-4 text-sm">
            {#each ["params", "settings"] as t}
              <button
                class="px-3 py-2 capitalize border-b-2 transition-colors"
                class:border-rose-500={activeTab === t}
                class:text-rose-600={activeTab === t}
                class:border-transparent={activeTab !== t}
                class:text-slate-500={activeTab !== t}
                onclick={() => (activeTab = t as typeof activeTab)}
              >{t === "params" ? "Parameters" : "Settings"}</button>
            {/each}
            <div class="flex-1"></div>
            <button class="my-1.5 inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-rose-500 hover:bg-rose-600 text-white text-xs font-medium">
              <span>▸</span> Execute step
            </button>
          </nav>

          <div class="p-4 space-y-3 text-sm">
            {#if activeTab === "params"}
              <div>
                <div class="text-[11px] text-slate-500 uppercase mb-1">Node ID</div>
                <div class="font-mono text-[12px] text-slate-700 dark:text-slate-300">{node.id}</div>
              </div>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Label</span>
                <input class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                       value={node.label ?? ""}
                       oninput={(e) => patch("label", (e.target as HTMLInputElement).value)} />
              </label>
              {#if node.type === "http"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Method</span>
                  <select class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5" value={node.method ?? "GET"} onchange={(e) => patch("method", (e.target as HTMLSelectElement).value)}>
                    {#each ["GET","POST","PUT","PATCH","DELETE"] as m}
                      <option value={m}>{m}</option>
                    {/each}
                  </select>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">URL</span>
                  <input class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono" value={node.url ?? ""} oninput={(e) => patch("url", (e.target as HTMLInputElement).value)} />
                </label>
              {/if}
              {#if node.type === "db_query"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Database</span>
                  <input class="rounded border px-3 py-1.5" value={node.database ?? ""} oninput={(e) => patch("database", (e.target as HTMLInputElement).value)} />
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">SQL</span>
                  <textarea class="rounded border px-3 py-1.5 font-mono min-h-[140px]" value={node.sql ?? ""} oninput={(e) => patch("sql", (e.target as HTMLTextAreaElement).value)}></textarea>
                </label>
              {/if}
              {#if node.type === "shell"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Command</span>
                  <input class="rounded border px-3 py-1.5 font-mono" value={(node.command ?? []).join(" ")} oninput={(e) => patch("command", (e.target as HTMLInputElement).value.split(/\s+/).filter(Boolean))} />
                </label>
              {/if}
              {#if node.type === "classify" || node.type === "agent"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Provider</span>
                  <input class="rounded border px-3 py-1.5" value={node.provider ?? ""} oninput={(e) => patch("provider", (e.target as HTMLInputElement).value)} />
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Prompt</span>
                  <textarea class="rounded border px-3 py-1.5 min-h-[140px] font-mono" value={node.prompt ?? ""} oninput={(e) => patch("prompt", (e.target as HTMLTextAreaElement).value)}></textarea>
                </label>
              {/if}
              {#if node.type === "branch"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Expression</span>
                  <input class="rounded border px-3 py-1.5 font-mono" value={node.expr ?? ""} oninput={(e) => patch("expr", (e.target as HTMLInputElement).value)} />
                </label>
              {/if}
              {#if node.type === "go_script" || node.type === "python"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Code</span>
                  <textarea class="rounded border px-3 py-1.5 font-mono min-h-[200px]" value={node.code ?? ""} oninput={(e) => patch("code", (e.target as HTMLTextAreaElement).value)}></textarea>
                </label>
              {/if}
              {#if node.type === "transform"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Engine</span>
                  <input class="rounded border px-3 py-1.5" value={node.engine ?? "template"} oninput={(e) => patch("engine", (e.target as HTMLInputElement).value)} />
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Expression</span>
                  <textarea class="rounded border px-3 py-1.5 font-mono min-h-[120px]" value={node.expression ?? ""} oninput={(e) => patch("expression", (e.target as HTMLTextAreaElement).value)}></textarea>
                </label>
              {/if}
            {:else}
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Timeout (sec)</span>
                <input type="number" class="rounded border px-3 py-1.5" value={node.timeout_sec ?? 0} oninput={(e) => patch("timeout_sec", Number((e.target as HTMLInputElement).value) || 0)} />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">On failure</span>
                <select class="rounded border px-3 py-1.5" value={node.on_failure ?? "halt"} onchange={(e) => patch("on_failure", (e.target as HTMLSelectElement).value)}>
                  <option value="halt">halt</option>
                  <option value="skip">skip</option>
                  <option value="fallback">fallback</option>
                </select>
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Fallback node</span>
                <input class="rounded border px-3 py-1.5 font-mono" value={node.fallback ?? ""} oninput={(e) => patch("fallback", (e.target as HTMLInputElement).value)} />
              </label>
            {/if}
          </div>
        </section>

        <!-- RIGHT: output. -->
        <section class="flex flex-col p-4 overflow-y-auto">
          <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2">OUTPUT</div>
          <div class="flex-1 flex flex-col items-center justify-center text-slate-400 text-xs gap-2">
            <div>Last recorded output unavailable.</div>
            <div class="text-[11px]">Run the workflow to populate.</div>
          </div>
        </section>
      </div>
    </div>
  </div>
{/if}
