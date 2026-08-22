<script lang="ts">
  /* One ticket: its state, the sessions working on it, and its notes.

     The session list plus "New session" is the reason tickets exist as
     their own entity — when a conversation goes wrong you start another one
     here, and the ticket's notes carry over. */
  import type { TicketDetail } from "../types/agents.js";
  import { deleteTicket, detachSession, getTicket, updateTicket } from "../api/tickets.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { timeAgo } from "../timeFormat.js";
  import NotesPanel from "./NotesPanel.svelte";

  type Props = {
    base: string;
    ticketId: string;
    onBack: () => void;
    onOpenSession: (sessionId: string) => void;
    /* Starts a new session already attached to this ticket. */
    onNewSession: (ticketId: string) => void;
  };

  let { base, ticketId, onBack, onOpenSession, onNewSession }: Props = $props();

  let data = $state<TicketDetail | null>(null);
  let error = $state("");

  function load() {
    Effect.runPromise(getTicket(base, ticketId).pipe(Effect.provide(WickClientLayer)))
      .then((d) => {
        data = d;
        error = "";
      })
      .catch((e: unknown) => {
        error = e instanceof Error ? e.message : "Failed to load ticket";
      });
  }

  $effect(() => {
    ticketId;
    load();
  });

  /* Statuses are the project's own, so labels come from the payload rather
     than a table here. */
  const labelOf = (key: string) =>
    data?.statuses.find((s) => s.key === key)?.label || key;
  const terminalKey = $derived(
    data?.statuses.find((s) => s.terminal)?.key ??
      data?.statuses[(data?.statuses.length ?? 0) - 1]?.key ??
      "",
  );
  const accents = [
    "bg-prog-100 text-prog-400",
    "bg-cau-100 text-cau-400",
    "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600",
    "bg-link-100 text-link-400",
  ];
  function pillFor(key: string): string {
    if (key === terminalKey) return "bg-pos-100 text-pos-400";
    const i = data?.statuses.findIndex((s) => s.key === key) ?? -1;
    return i < 0 ? "bg-white-300 text-black-800" : accents[i % accents.length];
  }

  function patch(body: Parameters<typeof updateTicket>[2]) {
    Effect.runPromise(updateTicket(base, ticketId, body).pipe(Effect.provide(WickClientLayer)))
      .then(load)
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to update ticket"));
  }

  function detach(sessionId: string) {
    Effect.runPromise(detachSession(base, ticketId, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then(load)
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to detach session"));
  }

  /* Title edits go through a draft so a keystroke is not a request. */
  let titleDraft = $state("");
  let editingTitle = $state(false);
  function startTitle() {
    titleDraft = data?.ticket.title ?? "";
    editingTitle = true;
  }
  function saveTitle() {
    editingTitle = false;
    const t = titleDraft.trim();
    if (t !== "" && t !== data?.ticket.title) patch({ title: t });
  }

  const assigneeName = $derived(
    data?.ticket.assignee ? (data.users?.[data.ticket.assignee] ?? data.ticket.assignee) : "",
  );

  /* ── deleting the ticket ──
     Two outcomes with very different weight, so they are two buttons rather
     than one with a checkbox: keeping the chats is routine, deleting them
     destroys conversations, notes, and working history for good. The
     confirmation names the count, because "3 conversations" is the fact a
     person needs to stop and read. */
  let confirmDelete = $state(false);
  let deleting = $state(false);

  function runDelete(sessions: "keep" | "delete") {
    if (!data) return;
    deleting = true;
    Effect.runPromise(
      deleteTicket(base, data.ticket.id, sessions).pipe(Effect.provide(WickClientLayer)),
    )
      .then(() => { confirmDelete = false; onBack(); })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to delete the ticket"))
      .finally(() => { deleting = false; });
  }
</script>

{#if error}
  <div class="rounded-xl border border-neg-400/40 bg-neg-100 px-4 py-3 text-sm text-neg-400">{error}</div>
{:else if data}
  {@const t = data.ticket}
  <div class="flex flex-col gap-4">
    <button
      type="button"
      onclick={onBack}
      class="inline-flex w-fit items-center gap-1.5 text-xs text-black-700 transition-colors hover:text-green-600 dark:text-black-600 dark:hover:text-green-400"
    >
      <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
        <path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"></path>
      </svg>
      Back to board
    </button>

    <!-- Header -->
    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <div class="flex items-start gap-3">
        <span class="rounded bg-white-200 px-2 py-1 font-mono text-xs font-semibold text-black-800 dark:bg-navy-800 dark:text-black-600">
          {t.id}
        </span>
        <div class="min-w-0 flex-1">
          {#if editingTitle}
            <input
              bind:value={titleDraft}
              onblur={saveTitle}
              onkeydown={(e) => { if (e.key === "Enter") saveTitle(); }}
              aria-label="Ticket title"
              class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1 text-base font-semibold text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
            />
          {:else}
            <button
              type="button"
              onclick={startTitle}
              class="w-full rounded text-left text-base font-semibold text-black-900 hover:bg-white-200 dark:text-white-100 dark:hover:bg-navy-800"
            >{t.title}</button>
          {/if}
          <p class="mt-1 text-xs text-black-700 dark:text-black-600">
            updated {timeAgo(t.updated_at)} · created {timeAgo(t.created_at)}
          </p>
        </div>
        <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold " + pillFor(t.status)}>
          {labelOf(t.status)}
        </span>
        <button
          type="button"
          onclick={() => { confirmDelete = true; }}
          class="shrink-0 rounded-lg border border-neg-400/40 px-2 py-1 text-[11px] font-medium text-neg-400 transition-colors hover:bg-neg-100"
        >Delete</button>
      </div>

      <div class="mt-4 grid gap-3 sm:grid-cols-2">
        <div>
          <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-status">Status</label>
          <select
            id="tkt-status"
            value={t.status}
            onchange={(e) => patch({ status: (e.target as HTMLSelectElement).value })}
            class="w-full rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          >
            {#each data.statuses as s (s.key)}
              <option value={s.key}>{s.label || s.key}</option>
            {/each}
          </select>
        </div>
        <div>
          <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-assignee">Assignee</label>
          <div class="flex gap-2">
            <input
              id="tkt-assignee"
              value={t.assignee ?? ""}
              onblur={(e) => patch({ assignee: (e.target as HTMLInputElement).value })}
              placeholder="unassigned"
              class="min-w-0 flex-1 rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
            />
            {#if data.me}
              <button
                type="button"
                onclick={() => patch({ assignee: data?.me })}
                class="shrink-0 rounded-lg border border-green-500 px-3 py-2 text-xs font-medium text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20"
              >Take it</button>
            {/if}
          </div>
          {#if assigneeName && assigneeName !== t.assignee}
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">{assigneeName}</p>
          {/if}
        </div>

        {#each data.config.fields ?? [] as f (f.key)}
          <div>
            <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for={"tkt-f-" + f.key}>
              {f.label || f.key}{#if f.required}<span class="text-neg-400"> *</span>{/if}
            </label>
            {#if f.type === "select"}
              <select
                id={"tkt-f-" + f.key}
                value={t.fields?.[f.key] ?? ""}
                onchange={(e) => patch({ fields: { [f.key]: (e.target as HTMLSelectElement).value } })}
                class="w-full rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
              >
                <option value="">—</option>
                {#each f.options ?? [] as opt (opt)}
                  <option value={opt}>{opt}</option>
                {/each}
              </select>
            {:else}
              <input
                id={"tkt-f-" + f.key}
                value={t.fields?.[f.key] ?? ""}
                onblur={(e) => patch({ fields: { [f.key]: (e.target as HTMLInputElement).value } })}
                class="w-full rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
              />
            {/if}
          </div>
        {/each}
      </div>
    </div>

    <!-- Sessions. Several per ticket is the normal case, not an edge one. -->
    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <div class="mb-3 flex items-start justify-between gap-3">
        <div>
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Sessions</h2>
          <p class="mt-1 text-xs text-black-700 dark:text-black-600">
            Conversations working on this ticket. Start a new one when a session has run out of
            road — the notes below carry over.
          </p>
        </div>
        <button
          type="button"
          onclick={() => onNewSession(t.id)}
          class="shrink-0 rounded-lg bg-green-600 px-3 py-1.5 text-xs font-semibold text-white-100 transition-colors hover:bg-green-700"
        >+ New session</button>
      </div>

      {#if data.sessions.length === 0}
        <p class="text-xs text-black-700 dark:text-black-600">No sessions yet.</p>
      {:else}
        <ul class="flex flex-col gap-2">
          {#each data.sessions as s (s.id)}
            <li class="flex items-center gap-3 rounded-lg border border-white-300 bg-white-100 px-4 py-2.5 dark:border-navy-600 dark:bg-navy-800">
              <button
                type="button"
                onclick={() => onOpenSession(s.id)}
                class="min-w-0 flex-1 text-left"
              >
                <span class="block truncate text-sm text-black-900 dark:text-white-100">
                  {s.label || s.id}
                </span>
                <span class="mt-0.5 flex items-center gap-2 text-[11px] text-black-700 dark:text-black-600">
                  <span class="font-mono">#{s.id.slice(0, 8)}</span>
                  {#if s.lifecycle}<span class="rounded bg-pos-100 px-1.5 py-0.5 text-pos-400">{s.lifecycle}</span>{/if}
                  {#if s.last_active}<span>{timeAgo(s.last_active)}</span>{/if}
                </span>
              </button>
              <button
                type="button"
                aria-label="Detach session"
                title="Detach from this ticket (the session itself is kept)"
                onclick={() => detach(s.id)}
                class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                  <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
                </svg>
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    <!-- Notes -->
    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Notes in this ticket's scope</h2>
      <p class="mb-3 mt-1 text-xs leading-relaxed text-black-700 dark:text-black-600">
        Notes are their own thing — a chat keeps them with or without a ticket. What this ticket
        changes is the scope: these are shared by every session on it. The agent does not get them
        in its prompt; it reads them through the notes connector, so a long ticket costs nothing
        per turn.
      </p>
      <NotesPanel
        {base}
        scope={{ ticketId: t.id }}
        notes={data.notes}
        users={data.users}
        onChanged={load}
      />
    </div>
  </div>
  <!-- Deleting the ticket. The two outcomes are separate buttons because
       one is routine and the other is not: keeping the chats loses only the
       ticket record, deleting them ends conversations for good. The count
       is spelled out rather than implied. -->
  {#if confirmDelete}
    {@const n = data.sessions.length}
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-navy-900/40 p-4">
      <div class="w-full max-w-md rounded-xl border border-white-300 bg-white-100 p-5 shadow-xl dark:border-navy-600 dark:bg-navy-700">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">
          Delete ticket {t.id}?
        </h2>

        {#if n === 0}
          <p class="mt-2 text-xs leading-relaxed text-black-700 dark:text-black-600">
            It holds no chats, so only the ticket and its notes go.
          </p>
          <div class="mt-4 flex justify-end gap-2">
            <button
              type="button"
              onclick={() => { confirmDelete = false; }}
              class="rounded-lg border border-white-400 px-3 py-1.5 text-xs text-black-800 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-800"
            >Cancel</button>
            <button
              type="button"
              disabled={deleting}
              onclick={() => runDelete("keep")}
              class="rounded-lg bg-neg-400 px-3 py-1.5 text-xs font-semibold text-white-100 transition-colors hover:bg-neg-500 disabled:opacity-40"
            >{deleting ? "Deleting…" : "Delete ticket"}</button>
          </div>
        {:else}
          <p class="mt-2 text-xs leading-relaxed text-black-700 dark:text-black-600">
            This ticket holds
            <strong class="text-black-900 dark:text-white-100">
              {n} conversation{n === 1 ? "" : "s"}
            </strong>. Choose what happens to {n === 1 ? "it" : "them"}.
          </p>

          <div class="mt-4 flex flex-col gap-2">
            <button
              type="button"
              disabled={deleting}
              onclick={() => runDelete("keep")}
              class="rounded-lg border border-white-400 px-3 py-2 text-left transition-colors hover:bg-white-200 disabled:opacity-40 dark:border-navy-600 dark:hover:bg-navy-800"
            >
              <span class="block text-xs font-semibold text-black-900 dark:text-white-100">
                Delete the ticket, keep the chats
              </span>
              <span class="mt-0.5 block text-[11px] text-black-700 dark:text-black-600">
                {n === 1 ? "It becomes" : "They become"} untracked. Nothing is lost.
              </span>
            </button>

            <button
              type="button"
              disabled={deleting}
              onclick={() => runDelete("delete")}
              class="rounded-lg border border-neg-400 px-3 py-2 text-left transition-colors hover:bg-neg-100 disabled:opacity-40"
            >
              <span class="block text-xs font-semibold text-neg-400">
                Delete the ticket and {n === 1 ? "the conversation" : `all ${n} conversations`}
              </span>
              <span class="mt-0.5 block text-[11px] text-black-700 dark:text-black-600">
                Their messages, notes, and files go too. This cannot be undone.
              </span>
            </button>
          </div>

          <div class="mt-3 flex justify-end">
            <button
              type="button"
              onclick={() => { confirmDelete = false; }}
              class="rounded-lg px-3 py-1.5 text-xs text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800"
            >Cancel</button>
          </div>
        {/if}
      </div>
    </div>
  {/if}
{:else}
  <p class="py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</p>
{/if}
