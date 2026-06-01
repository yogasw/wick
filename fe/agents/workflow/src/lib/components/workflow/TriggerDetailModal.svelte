<script lang="ts">
  // Trigger inspector — 3-column modal opened by double-clicking a
  // trigger card on the canvas. Mirrors NodeDetailModal's layout but
  // resolves the entity from `wf.triggers[]` and swaps the parameter
  // form per trigger.type (manual / cron / channel / webhook /
  // schedule_at / error). Field set matches the legacy templ
  // inspector — see
  // internal/tools/agents/view/workflow/editor_inspector.templ.
  import {
    detailTriggerID,
    draftWorkflow,
    removeTrigger,
    updateTrigger,
  } from "$lib/stores/editor";
  import type { Trigger } from "$lib/types/workflow";
  import { workflowAPI } from "$lib/api/workflow";

  const trigger = $derived.by<Trigger | null>(() => {
    const id = $detailTriggerID;
    if (!id || !$draftWorkflow) return null;
    return ($draftWorkflow.triggers ?? []).find((t) => t.id === id) ?? null;
  });

  let activeTab = $state<"params" | "settings">("params");

  function close() {
    detailTriggerID.set(null);
  }

  function patch(field: keyof Trigger, value: unknown) {
    if (!trigger?.id) return;
    updateTrigger(trigger.id, { [field]: value } as Partial<Trigger>);
  }

  function patchMatchEntry(key: string, value: unknown) {
    if (!trigger?.id) return;
    const next = { ...(trigger.match ?? {}) };
    if (key === "") return;
    next[key] = value;
    updateTrigger(trigger.id, { match: next });
  }

  function removeMatchEntry(key: string) {
    if (!trigger?.id) return;
    const next = { ...(trigger.match ?? {}) };
    delete next[key];
    updateTrigger(trigger.id, { match: next });
  }

  // Add-row local state for the channel match filter. Lives at the
  // component scope because there's only one editable add-row at a
  // time and resetting on submit is straightforward.
  let newMatchKey = $state("");
  let newMatchValue = $state("");
  function addMatchEntry() {
    const k = newMatchKey.trim();
    if (!k) return;
    patchMatchEntry(k, newMatchValue);
    newMatchKey = "";
    newMatchValue = "";
  }

  async function runManual() {
    const wf = $draftWorkflow;
    if (!wf?.id) return;
    try {
      await workflowAPI.runNow(wf.id);
    } catch (e) {
      // Non-fatal — toolbar surfaces last run status via lastRunSummary.
      console.warn("runNow failed", e);
    }
  }

  function onConfirmDelete() {
    if (!trigger?.id) return;
    removeTrigger(trigger.id);
    close();
  }

  const triggerHeadColour: Record<string, string> = {
    manual: "bg-amber-400",
    cron: "bg-sky-400",
    channel: "bg-emerald-400",
    webhook: "bg-violet-400",
    schedule_at: "bg-cyan-400",
    error: "bg-rose-500",
  };
</script>

<svelte:window onkeydown={(e) => trigger && e.key === "Escape" && close()} />

{#if trigger}
  <div
    class="fixed inset-0 z-50 bg-slate-900/70 backdrop-blur-sm"
    role="dialog"
    aria-modal="true"
    aria-label="Edit trigger"
    onclick={close}
  >
    <div
      class="rounded-lg overflow-hidden bg-white dark:bg-[#0f172a]
             text-slate-900 dark:text-slate-100 shadow-2xl flex flex-col"
      style="position:absolute; left:16px; right:16px; top:32px; bottom:32px;"
      onclick={(e) => e.stopPropagation()}
      role="presentation"
    >
      <!-- Header. -->
      <header class="flex items-center gap-3 px-5 py-3 border-b border-slate-200 dark:border-slate-700">
        <span class="h-2 w-2 rounded-full {triggerHeadColour[trigger.type] ?? 'bg-amber-400'}"></span>
        <span class="text-sm font-semibold">{trigger.label || trigger.type}</span>
        <span class="text-xs text-slate-500 font-mono">trigger · {trigger.type}</span>
        <div class="flex-1"></div>
        <button class="text-slate-400 hover:text-slate-100 text-xl leading-none" onclick={close} aria-label="Close">✕</button>
      </header>

      <!-- 3-column body. -->
      <div class="flex-1 grid divide-x divide-slate-200 dark:divide-slate-800 min-h-0" style="grid-template-columns: 1fr 2fr 1fr;">
        <!-- LEFT: trigger has no upstream by definition. -->
        <section class="flex flex-col p-4 overflow-y-auto">
          <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2">INPUT</div>
          <div class="flex-1 flex flex-col items-center justify-center text-slate-400 text-xs gap-3">
            <div class="text-2xl">⤓</div>
            <div>No upstream</div>
            <div class="text-[11px] text-center max-w-[180px]">
              Triggers are entry points — they receive event payloads, not node outputs.
            </div>
          </div>
        </section>

        <!-- MIDDLE: parameters + settings. -->
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
            {#if trigger.type === "manual"}
              <button
                class="my-1.5 inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-rose-500 hover:bg-rose-600 text-white text-xs font-medium"
                onclick={runManual}
                title="Fire this manual trigger now"
              >
                <span>▸</span> Run now
              </button>
            {/if}
          </nav>

          <div class="p-4 space-y-3 text-sm">
            {#if activeTab === "params"}
              <!-- Common: id + label always visible. -->
              <div>
                <div class="text-[11px] text-slate-500 uppercase mb-1">Trigger ID</div>
                <div class="font-mono text-[12px] text-slate-700 dark:text-slate-300 break-all">{trigger.id ?? "—"}</div>
              </div>
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Label</span>
                <input
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                  value={trigger.label ?? ""}
                  oninput={(e) => patch("label", (e.target as HTMLInputElement).value)}
                />
              </label>

              <!-- Cron-specific fields. -->
              {#if trigger.type === "cron"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Schedule (cron)</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="0 */15 * * * *"
                    value={trigger.schedule ?? ""}
                    oninput={(e) => patch("schedule", (e.target as HTMLInputElement).value)}
                  />
                  <span class="text-[11px] text-slate-500">6-field cron: sec min hour dom mon dow</span>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Timezone</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="Asia/Jakarta"
                    value={trigger.timezone ?? ""}
                    oninput={(e) => patch("timezone", (e.target as HTMLInputElement).value)}
                  />
                </label>
              {/if}

              <!-- Webhook-specific fields. -->
              {#if trigger.type === "webhook"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Path</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="/hooks/my-hook"
                    value={trigger.path ?? ""}
                    oninput={(e) => patch("path", (e.target as HTMLInputElement).value)}
                  />
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Method</span>
                  <select
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    value={trigger.method ?? ""}
                    onchange={(e) => patch("method", (e.target as HTMLSelectElement).value)}
                  >
                    <option value="">(any)</option>
                    {#each ["GET", "POST", "PUT", "DELETE", "PATCH"] as m}
                      <option value={m}>{m}</option>
                    {/each}
                  </select>
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Secret ref (optional)</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="env:WEBHOOK_SECRET"
                    value={trigger.secret_ref ?? ""}
                    oninput={(e) => patch("secret_ref", (e.target as HTMLInputElement).value)}
                  />
                </label>
              {/if}

              <!-- Manual-specific fields. -->
              {#if trigger.type === "manual"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Button label</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    placeholder="Run now"
                    value={trigger.button_label ?? ""}
                    oninput={(e) => patch("button_label", (e.target as HTMLInputElement).value)}
                  />
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Require role (optional)</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5"
                    placeholder="admin"
                    value={trigger.require_role ?? ""}
                    oninput={(e) => patch("require_role", (e.target as HTMLInputElement).value)}
                  />
                </label>
              {/if}

              <!-- Channel-specific fields. Channel + event are free-text
                   inputs for now; the legacy editor wires them to a
                   dynamic catalog (channels list + per-channel event
                   schema). Catalog wiring lands in a follow-up. -->
              {#if trigger.type === "channel"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Channel</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="slack / telegram / rest"
                    value={trigger.channel ?? ""}
                    oninput={(e) => patch("channel", (e.target as HTMLInputElement).value)}
                  />
                </label>
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Event</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="message"
                    value={trigger.event ?? ""}
                    oninput={(e) => patch("event", (e.target as HTMLInputElement).value)}
                  />
                </label>
                <label class="flex items-center gap-2 mt-2">
                  <input
                    type="checkbox"
                    class="w-4 h-4 accent-emerald-500"
                    checked={trigger.match_enabled ?? false}
                    onchange={(e) => patch("match_enabled", (e.target as HTMLInputElement).checked)}
                  />
                  <span class="text-xs font-medium">Filter events (whitelist match)</span>
                </label>
                {#if trigger.match_enabled}
                  <div class="rounded border border-slate-200 dark:border-slate-700 p-2 space-y-2">
                    <div class="text-[11px] text-slate-500">Match keys — exact-string filter on the event payload.</div>
                    {#each Object.entries(trigger.match ?? {}) as [k, v] (k)}
                      <div class="flex items-center gap-2">
                        <input
                          class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
                          value={k}
                          readonly
                        />
                        <input
                          class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
                          value={typeof v === "string" ? v : JSON.stringify(v)}
                          oninput={(e) => patchMatchEntry(k, (e.target as HTMLInputElement).value)}
                        />
                        <button
                          class="text-rose-500 text-xs px-2"
                          onclick={() => removeMatchEntry(k)}
                          title="Remove match key"
                        >✕</button>
                      </div>
                    {/each}
                    <div class="flex items-center gap-2 pt-1 border-t border-slate-200 dark:border-slate-700">
                      <input
                        class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
                        placeholder="key"
                        bind:value={newMatchKey}
                      />
                      <input
                        class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono text-[12px] flex-1"
                        placeholder="value"
                        bind:value={newMatchValue}
                        onkeydown={(e) => e.key === "Enter" && addMatchEntry()}
                      />
                      <button
                        class="text-emerald-600 text-xs px-2"
                        onclick={addMatchEntry}
                        title="Add match key"
                      >+</button>
                    </div>
                  </div>
                {/if}
              {/if}

              <!-- schedule_at-specific fields. -->
              {#if trigger.type === "schedule_at"}
                <label class="flex flex-col gap-1">
                  <span class="text-xs font-medium">Fire at (ISO 8601)</span>
                  <input
                    class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                    placeholder="2026-12-31T15:00:00Z"
                    value={trigger.at ?? ""}
                    oninput={(e) => patch("at", (e.target as HTMLInputElement).value)}
                  />
                </label>
                <label class="flex items-center gap-2 mt-2">
                  <input
                    type="checkbox"
                    class="w-4 h-4 accent-emerald-500"
                    checked={trigger.delete_after ?? false}
                    onchange={(e) => patch("delete_after", (e.target as HTMLInputElement).checked)}
                  />
                  <span class="text-xs font-medium">Delete trigger after firing</span>
                </label>
              {/if}

              <!-- error-specific fields: no extra knobs, just the entry-node binding. -->
              {#if trigger.type === "error"}
                <div class="text-[11px] text-slate-500 italic mt-2">
                  Error triggers run when any other trigger fails. They take
                  the failed run's error payload as input — wire the entry
                  node from the trigger output port.
                </div>
              {/if}

              <!-- Danger zone. -->
              <button
                class="mt-6 px-3 py-1.5 rounded border border-rose-500 text-rose-600 hover:bg-rose-50 dark:hover:bg-rose-950 text-xs font-medium"
                onclick={onConfirmDelete}
              >Delete trigger</button>
            {:else}
              <!-- Settings tab — generic knobs that apply across types. -->
              <label class="flex flex-col gap-1">
                <span class="text-xs font-medium">Entry node ID</span>
                <input
                  class="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-800 px-3 py-1.5 font-mono"
                  placeholder="(set by drawing an edge from the trigger output port)"
                  value={trigger.entry_node ?? ""}
                  oninput={(e) => patch("entry_node", (e.target as HTMLInputElement).value)}
                />
                <span class="text-[11px] text-slate-500">
                  Usually populated by the canvas when you connect the trigger
                  to a node. Edit only if you want to force the entry without
                  redrawing the edge.
                </span>
              </label>
            {/if}
          </div>
        </section>

        <!-- RIGHT: last received event preview. Empty until the SSE
             stream surfaces a payload — same n8n-style affordance as
             NodeDetailModal's output column. -->
        <section class="flex flex-col p-4 overflow-y-auto">
          <div class="text-[11px] font-semibold tracking-wider text-slate-500 mb-2">OUTPUT</div>
          <div class="flex-1 flex flex-col items-center justify-center text-slate-400 text-xs gap-3">
            <div class="text-2xl">⤒</div>
            <div>No event data</div>
            <div class="text-[11px] text-center max-w-[180px]">
              Last-fired event payload surfaces here once a run lands.
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>
{/if}
