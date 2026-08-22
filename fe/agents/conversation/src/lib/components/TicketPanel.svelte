<script lang="ts">
  import type { SessionTicket } from "../api/tickets.js";
  import { updateSessionTicket } from "../api/tickets.js";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { timeAgo } from "../timeFormat.js";

  type Props = {
    base: string;
    sessionId: string;
    info: SessionTicket;
    /* Called after a successful save so the host can refresh its copy. */
    onSaved: () => void;
  };

  let { base, sessionId, info, onSaved }: Props = $props();

  const statusLabels: Record<string, string> = {
    open: "Open",
    in_progress: "In Progress",
    waiting: "Waiting",
    done: "Done",
  };
  const statusPill: Record<string, string> = {
    open: "bg-prog-100 text-prog-400",
    in_progress: "bg-cau-100 text-cau-400",
    waiting: "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600",
    done: "bg-pos-100 text-pos-400",
  };

  /* Editable draft, re-seeded whenever the host hands us fresh info. */
  let status = $state("open");
  let assignee = $state("");
  let fields = $state<Record<string, string>>({});
  let saving = $state(false);

  $effect(() => {
    status = info.ticket?.status ?? "open";
    assignee = info.ticket?.assignee ?? "";
    fields = { ...(info.ticket?.fields ?? {}) };
  });

  const dirty = $derived(
    status !== (info.ticket?.status ?? "open") ||
      assignee !== (info.ticket?.assignee ?? "") ||
      JSON.stringify(fields) !== JSON.stringify(info.ticket?.fields ?? {}),
  );

  const assigneeName = $derived(assignee ? (info.users?.[assignee] ?? assignee) : "");

  function save() {
    saving = true;
    Effect.runPromise(
      updateSessionTicket(base, sessionId, { status, assignee, fields }).pipe(
        Effect.provide(WickClientLayer),
      ),
    )
      .then(() => {
        toastOk("Ticket updated");
        onSaved();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to save ticket"))
      .finally(() => {
        saving = false;
      });
  }
</script>

<div class="flex h-full flex-col overflow-y-auto p-4">
  <div class="flex items-center justify-between">
    <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Ticket</h3>
    <span class={"rounded-full px-2 py-0.5 text-[10px] font-semibold " + (statusPill[status] ?? "")}>
      {statusLabels[status] ?? status}
    </span>
  </div>

  <label class="mt-4 block text-xs font-medium text-black-800 dark:text-black-600" for="ticket-status">Status</label>
  <select
    id="ticket-status"
    bind:value={status}
    class="mt-1 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
  >
    {#each info.statuses as s (s)}
      <option value={s}>{statusLabels[s] ?? s}</option>
    {/each}
  </select>

  <label class="mt-4 block text-xs font-medium text-black-800 dark:text-black-600" for="ticket-assignee">Assignee</label>
  <div class="mt-1 flex gap-2">
    <input
      id="ticket-assignee"
      bind:value={assignee}
      placeholder="user id"
      class="min-w-0 flex-1 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:outline-none"
    />
    {#if info.me}
      <button
        type="button"
        onclick={() => { assignee = info.me!; }}
        class="shrink-0 rounded-lg border border-green-500 px-3 py-2 text-xs font-medium text-green-600 dark:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors"
      >Assign to me</button>
    {/if}
  </div>
  {#if assigneeName && assigneeName !== assignee}
    <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">{assigneeName}</p>
  {/if}

  {#each info.config.fields ?? [] as f (f.key)}
    <label class="mt-4 block text-xs font-medium text-black-800 dark:text-black-600" for={"ticket-f-" + f.key}>
      {f.label || f.key}{#if f.required}<span class="text-neg-400"> *</span>{/if}
    </label>
    {#if f.type === "select"}
      <select
        id={"ticket-f-" + f.key}
        value={fields[f.key] ?? ""}
        onchange={(e) => { fields = { ...fields, [f.key]: (e.target as HTMLSelectElement).value }; }}
        class="mt-1 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
      >
        <option value="">—</option>
        {#each f.options ?? [] as opt (opt)}
          <option value={opt}>{opt}</option>
        {/each}
      </select>
    {:else}
      <input
        id={"ticket-f-" + f.key}
        value={fields[f.key] ?? ""}
        oninput={(e) => { fields = { ...fields, [f.key]: (e.target as HTMLInputElement).value }; }}
        class="mt-1 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
      />
    {/if}
  {/each}

  <p class="mt-4 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
    {#if info.ticket?.updated_at}
      Last ticket update {timeAgo(info.ticket.updated_at)}.
    {:else}
      Not tracked yet — saving makes this session a ticket.
    {/if}
    The agent can update this too via <span class="font-mono">wick_ticket_set</span>.
  </p>

  <button
    type="button"
    disabled={!dirty || saving}
    onclick={save}
    class="mt-4 w-full rounded-lg bg-green-600 px-4 py-2 text-sm font-semibold text-white-100 transition-colors hover:bg-green-700 active:bg-green-800 disabled:opacity-40 disabled:cursor-default"
  >
    {saving ? "Saving…" : "Save"}
  </button>
</div>
