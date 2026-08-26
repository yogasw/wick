<script lang="ts">
  /* One ticket: its state, the sessions working on it, and its notes.

     The session list plus "New session" is the reason tickets exist as
     their own entity — when a conversation goes wrong you start another one
     here, and the ticket's notes carry over. */
  import type { TicketButton, TicketDetail } from "../types/agents.js";
  import { deleteTicket, detachSession, getTicket, runTicketAction, updateTicket } from "../api/tickets.js";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { timeAgo } from "../timeFormat.js";
  import { renderMarkdown } from "../markdown.js";
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

  /* ── description (markdown) ──
     Edits go through a draft, same contract as the title: a keystroke is
     not a request, and Cancel throws the draft away. */
  let editingBody = $state(false);
  let bodyDraft = $state("");
  function startBody() {
    bodyDraft = data?.ticket.body ?? "";
    editingBody = true;
  }
  function saveBody() {
    editingBody = false;
    const b = bodyDraft.trim();
    if (b !== (data?.ticket.body ?? "")) patch({ body: b });
  }

  /* ── fields, capped ──
     The page draws the schema fields first (editable, typed), then any
     value written outside the schema — the REST surface accepts any key.
     Only the first three show by default; a ticket mirrored from another
     system can carry a dozen properties, and stacking them all would bury
     the sessions below. */
  const FIELDS_SHOWN = 3;
  let showAllFields = $state(false);
  const fieldDefs = $derived.by(() => {
    const schema = data?.config.fields ?? [];
    const values = data?.ticket.fields ?? {};
    const extras = Object.keys(values)
      .filter((k) => values[k] && !schema.some((d) => d.key === k))
      .sort()
      .map((k) => ({ key: k, label: k, type: "text" as const, options: [] as string[] }));
    return [...schema, ...extras];
  });
  const visibleFieldDefs = $derived(showAllFields ? fieldDefs : fieldDefs.slice(0, FIELDS_SHOWN));

  /* ── sessions, capped ──
     A long-lived ticket collects conversations; five is what fits a glance.
     The rest sit behind "show more" rather than stretching the page — same
     contract as the fields. */
  const SESSIONS_SHOWN = 5;
  let showAllSessions = $state(false);
  const visibleSessions = $derived(
    showAllSessions ? (data?.sessions ?? []) : (data?.sessions ?? []).slice(0, SESSIONS_SHOWN),
  );

  /* ── custom buttons ──
     One click, one delivery, one answer. The id doubles as the busy flag so
     a slow receiver cannot be double-fired. */
  let actionBusy = $state("");
  function runAction(b: TicketButton) {
    if (!b.id || actionBusy !== "") return;
    actionBusy = b.id;
    Effect.runPromise(
      runTicketAction(base, ticketId, b.id).pipe(Effect.provide(WickClientLayer)),
    )
      .then((r) => {
        if (r.ok) toastOk(`${b.label}: delivered (HTTP ${r.status})`);
        else toastError(`${b.label} failed: ${r.error || "HTTP " + r.status}`);
      })
      .catch((e: unknown) =>
        toastError(e instanceof Error ? e.message : `${b.label} failed`),
      )
      .finally(() => {
        actionBusy = "";
      });
  }

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

    <!-- Two panes: the work reads down the main column — title, description,
         conversations, then the notes as the comment thread (notes are long
         and wide by nature, so they get the width). The ticket's state sits
         in the properties rail on the right; on a phone it drops below. -->
    <div class="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_300px]">
    <div class="flex min-w-0 flex-col gap-4">
    <!-- Header. Identity first (code + status), then the title with room to
         be a sentence — the layout every ticketing tool converged on. -->
    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <div class="flex items-center gap-2">
        <!-- An adopted external id can be 32+ characters; it truncates with
             the full code on hover rather than squeezing the row out. -->
        <span
          title={t.id}
          class="min-w-0 truncate rounded bg-white-200 px-2 py-0.5 font-mono text-[11px] font-semibold text-black-800 dark:bg-navy-800 dark:text-black-600"
        >
          {t.id}
        </span>
        <span class={"shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold " + pillFor(t.status)}>
          {labelOf(t.status)}
        </span>
        <!-- Delete lives top-right as a quiet trash icon; the confirm
             dialog carries the gravity. -->
        <button
          type="button"
          aria-label="Delete ticket"
          title="Delete ticket"
          onclick={() => { confirmDelete = true; }}
          class="ml-auto flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600"
        >
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M2.5 4h11M6.5 4V2.5h3V4M4 4l.7 9a1 1 0 001 .9h4.6a1 1 0 001-.9L12 4" stroke-linecap="round" stroke-linejoin="round"></path>
            <path d="M6.5 7v4M9.5 7v4" stroke-linecap="round"></path>
          </svg>
        </button>
      </div>
      <div class="mt-2">
        {#if editingTitle}
          <input
            bind:value={titleDraft}
            onblur={saveTitle}
            onkeydown={(e) => { if (e.key === "Enter") saveTitle(); }}
            aria-label="Ticket title"
            class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1 text-xl font-semibold text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          />
        {:else}
          <button
            type="button"
            onclick={startTitle}
            title="Click to rename"
            class="w-full break-words rounded text-left text-xl font-semibold leading-snug text-black-900 hover:bg-white-200 dark:text-white-100 dark:hover:bg-navy-800"
          >{t.title}</button>
        {/if}
      </div>
    </div>

    <!-- Description. Markdown, because repro steps and links deserve
         better than a flat string. Click to edit, same as the title. -->
    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
        <div class="mb-1 flex items-center justify-between">
          <span class="text-xs font-medium text-black-800 dark:text-black-600">Description</span>
          {#if !editingBody && t.body}
            <button
              type="button"
              onclick={startBody}
              class="rounded px-2 py-0.5 text-[11px] text-black-700 transition-colors hover:text-green-600 dark:text-black-600 dark:hover:text-green-400"
            >Edit</button>
          {/if}
        </div>
        {#if editingBody}
          <textarea
            bind:value={bodyDraft}
            rows="6"
            placeholder="Markdown — repro steps, links, context…"
            aria-label="Ticket description"
            class="w-full rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          ></textarea>
          <div class="mt-2 flex justify-end gap-2">
            <button
              type="button"
              onclick={() => { editingBody = false; }}
              class="rounded-lg px-3 py-1.5 text-xs text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800"
            >Cancel</button>
            <button
              type="button"
              onclick={saveBody}
              class="rounded-lg bg-green-600 px-3 py-1.5 text-xs font-semibold text-white-100 transition-colors hover:bg-green-700"
            >Save</button>
          </div>
        {:else if t.body}
          <div class="wick-note-md break-words rounded-lg bg-white-200 px-3 py-2 text-sm text-black-900 dark:bg-navy-800 dark:text-white-100">
            {@html renderMarkdown(t.body)}
          </div>
        {:else}
          <button
            type="button"
            onclick={startBody}
            class="w-full rounded-lg border border-dashed border-white-400 px-3 py-2 text-left text-xs text-black-700 transition-colors hover:border-green-500 hover:text-green-600 dark:border-navy-600 dark:text-black-600 dark:hover:text-green-400"
          >+ Add a description</button>
        {/if}
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
          {#each visibleSessions as s (s.id)}
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
        {#if data.sessions.length > SESSIONS_SHOWN}
          <button
            type="button"
            onclick={() => { showAllSessions = !showAllSessions; }}
            class="mt-2 w-fit rounded px-1 py-0.5 text-[11px] text-black-700 transition-colors hover:text-green-600 dark:text-black-600 dark:hover:text-green-400"
          >
            {showAllSessions
              ? "Show less"
              : `Show ${data.sessions.length - SESSIONS_SHOWN} more session${data.sessions.length - SESSIONS_SHOWN === 1 ? "" : "s"}`}
          </button>
        {/if}
      {/if}
    </div>

    <!-- Notes: the comment thread. Long and wide by nature, so it sits at
         the bottom of the main column with the full width, not in a rail. -->
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

    <!-- Properties: the field panel on the right, a card like everything
         else on the page — no divider lines, whitespace does the grouping.
         Sticky, so the state stays in reach on a long thread. -->
    <aside class="flex flex-col gap-4 lg:sticky lg:top-4">
      <div class="rounded-xl border border-white-300 bg-white-100 p-4 shadow-sm dark:border-navy-600 dark:bg-navy-700">
        <h2 class="mb-3 text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Details</h2>
        <div class="flex flex-col gap-3">
          <div>
            <label class="mb-1 block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-status">Status</label>
            <select
              id="tkt-status"
              value={t.status}
              onchange={(e) => patch({ status: (e.target as HTMLSelectElement).value })}
              class="w-full rounded-lg border border-white-400 bg-white-100 px-2.5 py-1.5 text-xs text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
            >
              {#each data.statuses as s (s.key)}
                <option value={s.key}>{s.label || s.key}</option>
              {/each}
            </select>
          </div>

          <div>
            <!-- "take it" is a link beside the label, the Zendesk idiom —
                 claiming a ticket is one click, not a form control. -->
            <div class="mb-1 flex items-center justify-between">
              <label class="block text-xs font-medium text-black-800 dark:text-black-600" for="tkt-assignee">Assignee</label>
              {#if data.me}
                <button
                  type="button"
                  onclick={() => patch({ assignee: data?.me })}
                  class="text-[11px] font-medium text-green-600 transition-colors hover:underline dark:text-green-400"
                >take it</button>
              {/if}
            </div>
            <input
              id="tkt-assignee"
              value={t.assignee ?? ""}
              onblur={(e) => patch({ assignee: (e.target as HTMLInputElement).value })}
              placeholder="unassigned"
              class="w-full rounded-lg border border-white-400 bg-white-100 px-2.5 py-1.5 text-xs text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
            />
            {#if assigneeName && assigneeName !== t.assignee}
              <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">{assigneeName}</p>
            {/if}
          </div>

          {#each visibleFieldDefs as f (f.key)}
            <div class="min-w-0">
              <label class="mb-1 block truncate text-xs font-medium text-black-800 dark:text-black-600" for={"tkt-f-" + f.key} title={f.label || f.key}>
                {f.label || f.key}{#if "required" in f && f.required}<span class="text-neg-400"> *</span>{/if}
              </label>
              {#if f.type === "select"}
                <select
                  id={"tkt-f-" + f.key}
                  value={t.fields?.[f.key] ?? ""}
                  onchange={(e) => patch({ fields: { [f.key]: (e.target as HTMLSelectElement).value } })}
                  class="w-full rounded-lg border border-white-400 bg-white-100 px-2.5 py-1.5 text-xs text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
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
                  class="w-full rounded-lg border border-white-400 bg-white-100 px-2.5 py-1.5 text-xs text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
                />
              {/if}
            </div>
          {/each}

          {#if fieldDefs.length > FIELDS_SHOWN}
            <button
              type="button"
              onclick={() => { showAllFields = !showAllFields; }}
              class="w-fit rounded px-1 py-0.5 text-left text-[11px] text-black-700 transition-colors hover:text-green-600 dark:text-black-600 dark:hover:text-green-400"
            >
              {showAllFields
                ? "Show less"
                : `Show ${fieldDefs.length - FIELDS_SHOWN} more field${fieldDefs.length - FIELDS_SHOWN === 1 ? "" : "s"}`}
            </button>
          {/if}
        </div>

        <!-- Custom buttons (configured under Integrations). Each POSTs this
             ticket to its own URL — "Sync to Notion" and friends — and
             reports whether the receiver took it. -->
        {#if (data.config.integrations?.buttons ?? []).length > 0}
          <div class="mt-4 flex flex-col gap-2">
            {#each data.config.integrations?.buttons ?? [] as b (b.id)}
              <button
                type="button"
                disabled={actionBusy !== ""}
                onclick={() => runAction(b)}
                class="w-full rounded-lg border border-white-400 px-3 py-2 text-xs font-medium text-black-800 transition-colors hover:border-green-500 hover:text-green-600 disabled:opacity-40 dark:border-navy-600 dark:text-black-600 dark:hover:text-green-400"
              >{actionBusy === b.id ? "Sending…" : b.label}</button>
            {/each}
          </div>
        {/if}

        <p class="mt-4 text-[11px] leading-relaxed text-black-600 dark:text-black-700">
          updated {timeAgo(t.updated_at)}<br />created {timeAgo(t.created_at)}
        </p>
      </div>
    </aside>
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
