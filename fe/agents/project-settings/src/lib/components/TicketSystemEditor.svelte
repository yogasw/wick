<script lang="ts">
  import type { TicketConfig, TicketField } from "$lib/types.js";

  type Props = {
    cfg: TicketConfig;
    onChange: (cfg: TicketConfig) => void;
  };

  let { cfg, onChange }: Props = $props();

  const enabled = $derived(cfg.enabled === true);
  const fields = $derived(cfg.fields ?? []);
  const followupMin = $derived(Math.round((cfg.followup_after_sec ?? 0) / 60));
  const resolveDays = $derived(
    Math.round(((cfg.auto_resolve_after_sec ?? 0) / 86400) * 10) / 10,
  );

  function patch(p: Partial<TicketConfig>) {
    onChange({ ...cfg, ...p });
  }

  function patchField(i: number, p: Partial<TicketField>) {
    const next = fields.map((f, idx) => (idx === i ? { ...f, ...p } : f));
    patch({ fields: next });
  }

  function addField() {
    patch({ fields: [...fields, { key: "", label: "", type: "text" }] });
  }

  function removeField(i: number) {
    patch({ fields: fields.filter((_, idx) => idx !== i) });
  }

  /* Options edit as comma-separated text; parsed on the way out. */
  function setOptions(i: number, raw: string) {
    patchField(i, {
      options: raw.split(",").map((s) => s.trim()).filter((s) => s !== ""),
    });
  }
</script>

<div class="space-y-4">
  <div class="flex items-center justify-between">
    <div>
      <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Ticket system</h3>
      <p class="mt-1 text-xs text-black-700 dark:text-black-600">
        Treat every session in this project as a ticket card: status, assignee, custom
        fields, and a kanban board on the project page.
      </p>
    </div>
    <button
      type="button"
      role="switch"
      aria-checked={enabled}
      aria-label="Enable ticket system"
      onclick={() => patch({ enabled: !enabled })}
      class={"relative h-6 w-11 shrink-0 rounded-full transition-colors " + (enabled ? "bg-green-500" : "bg-white-400 dark:bg-navy-600")}
    >
      <span class={"absolute top-0.5 h-5 w-5 rounded-full bg-white-100 shadow-sm transition-all " + (enabled ? "left-[22px]" : "left-0.5")}></span>
    </button>
  </div>

  {#if enabled}
    <!-- Field schema -->
    <div class="space-y-2">
      <p class="text-xs font-medium text-black-800 dark:text-black-600">Custom fields</p>
      <div class="hidden sm:grid grid-cols-[1fr_1fr_88px_1.2fr_48px_32px] gap-2 text-[10px] uppercase tracking-wide text-black-600 dark:text-black-700">
        <span>Key</span><span>Label</span><span>Type</span><span>Options (comma-separated)</span><span>Req.</span><span></span>
      </div>
      {#each fields as f, i (i)}
        <div class="grid grid-cols-2 sm:grid-cols-[1fr_1fr_88px_1.2fr_48px_32px] gap-2 items-center">
          <input
            value={f.key}
            placeholder="key"
            aria-label="Field key"
            oninput={(e) => patchField(i, { key: (e.target as HTMLInputElement).value })}
            class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs font-mono text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
          />
          <input
            value={f.label}
            placeholder="Label"
            aria-label="Field label"
            oninput={(e) => patchField(i, { label: (e.target as HTMLInputElement).value })}
            class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
          />
          <select
            value={f.type}
            aria-label="Field type"
            onchange={(e) => patchField(i, { type: (e.target as HTMLSelectElement).value as "text" | "select" })}
            class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
          >
            <option value="text">text</option>
            <option value="select">select</option>
          </select>
          <input
            value={(f.options ?? []).join(", ")}
            placeholder={f.type === "select" ? "low, normal, high" : "—"}
            disabled={f.type !== "select"}
            aria-label="Field options"
            oninput={(e) => setOptions(i, (e.target as HTMLInputElement).value)}
            class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none disabled:opacity-40"
          />
          <label class="flex items-center justify-center">
            <input
              type="checkbox"
              checked={f.required === true}
              aria-label="Field required"
              onchange={(e) => patchField(i, { required: (e.target as HTMLInputElement).checked })}
              class="h-4 w-4 rounded border-white-400 dark:border-navy-600 text-green-600 focus:ring-green-500"
            />
          </label>
          <button
            type="button"
            aria-label="Remove field"
            onclick={() => removeField(i)}
            class="flex h-7 w-7 items-center justify-center rounded-lg text-black-600 dark:text-black-700 hover:bg-neg-100 hover:text-neg-400 transition-colors"
          >
            <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
            </svg>
          </button>
        </div>
      {/each}
      <button
        type="button"
        onclick={addField}
        class="rounded-lg border border-green-500 px-3 py-1.5 text-xs font-medium text-green-600 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
      >+ Add field</button>
    </div>

    <!-- Automation -->
    <div class="grid gap-4 sm:grid-cols-2">
      <div>
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-followup">
          Follow up when stale (minutes, 0 = off)
        </label>
        <input
          id="tkt-followup"
          type="number"
          min="0"
          value={followupMin}
          oninput={(e) => patch({ followup_after_sec: Math.max(0, Number((e.target as HTMLInputElement).value) || 0) * 60 })}
          class="mt-1 w-32 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
        />
      </div>
      <div>
        <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-resolve">
          Auto-resolve when idle (days, 0 = off)
        </label>
        <input
          id="tkt-resolve"
          type="number"
          min="0"
          step="0.5"
          value={resolveDays}
          oninput={(e) => patch({ auto_resolve_after_sec: Math.round(Math.max(0, Number((e.target as HTMLInputElement).value) || 0) * 86400) })}
          class="mt-1 w-32 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
        />
      </div>
    </div>

    <div>
      <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-prompt">
        Follow-up prompt
      </label>
      <textarea
        id="tkt-prompt"
        rows="3"
        value={cfg.followup_prompt ?? ""}
        placeholder="Check this ticket's latest state, post a short update to the ops channel, and update the ticket status."
        oninput={(e) => patch({ followup_prompt: (e.target as HTMLTextAreaElement).value })}
        class="mt-1 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:outline-none"
      ></textarea>
      <p class="mt-1 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
        When a ticket goes stale, the session's agent is woken with this prompt and
        decides what to do (escalate to a channel, ping the assignee, close it).
        Not a message to the user — an instruction to the agent.
      </p>
    </div>
  {/if}
</div>
