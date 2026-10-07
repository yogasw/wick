<script lang="ts">
  /* The rail's Ticket tab — about the ticket only. Notes live in their own
     tab, because notes are not a ticket feature: they work on a chat with no
     ticket at all, and a ticket only changes whose notes these are.

     Two states:
     - ON a ticket: shows it, lets the title be fixed, and lets this chat be
       moved to a different ticket or taken off tickets entirely.
     - On nothing: offers to create a ticket from this chat, or attach it to
       an existing one. */
  import type { Note, TicketCard, TicketField, TicketStatus } from "../types/agents.js";
  import NotesPanel from "./NotesPanel.svelte";
  import TicketFields from "./TicketFields.svelte";
  import FoldToggle from "./FoldToggle.svelte";
  import { renderMarkdown } from "../markdown.js";
  import "../notesMarkdown.css";
  import {
    attachSession,
    createTicket,
    detachSession,
    getProjectTickets,
    runTicketAction,
    updateTicket,
  } from "../api/tickets.js";
  import { rankTickets, shortTicketId } from "../ticketPick.js";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";

  type Props = {
    base: string;
    sessionId: string;
    /* Project the session belongs to; needed to create or list tickets. */
    projectId?: string;
    /* Ticket this session is attached to, when any. `body` is its markdown
       description — what was ASKED, which belongs beside the notes saying
       what was found. */
    ticket?: { id: string; title: string; status: string; body?: string; fields?: Record<string, string> | null } | null;
    /* The project's ticket field definitions. The rail shows and edits the
       ticket's values for these, in this order; none means no section. */
    fields?: TicketField[];
    /* The project's custom buttons placed on a ticket — typically "Sync from
       Notion". Clicked through /actions/{id}; the URL never reaches here. */
    buttons?: { id: string; label: string }[];
    /* The project's board columns, so the rail offers the same choices as
       the board rather than the built-in four. */
    statuses?: TicketStatus[];
    /* How many notes the resolved scope holds — the count on the section
       header below, and what the collapsed state reports. */
    noteCount?: number;
    /* The notes themselves, so the section can be read and written without
       leaving the ticket. Seeded from what the rail already fetched; the
       panel reloads on its own if nothing is passed. */
    notes?: Note[];
    users?: Record<string, string>;
    /* Opens the ticket's own page (sessions, fields, full note list). */
    onOpenTicket?: (ticketId: string) => void;
    /* Switches the rail to the Notes tab — passed only while that tab
       exists. With ticket mode on there is no separate Notes tab: this panel
       IS both, and a link to a tab that is not in the strip is a dead end. */
    onOpenNotes?: () => void;
    onChanged?: () => void;
  };

  let {
    base,
    sessionId,
    projectId,
    ticket,
    statuses,
    fields,
    buttons,
    noteCount = 0,
    notes,
    users,
    onOpenTicket,
    onOpenNotes,
    onChanged,
  }: Props = $props();

  /* The rail does not fetch the board, so its status choices arrive with
     the ticket. Falling back to the built-in set keeps the select usable
     against an older server that sends none. */
  const BUILTIN: TicketStatus[] = [
    { key: "open", label: "Open" },
    { key: "in_progress", label: "In Progress" },
    { key: "waiting", label: "Waiting" },
    { key: "done", label: "Done", terminal: true },
  ];
  const statusList = $derived(statuses && statuses.length > 0 ? statuses : BUILTIN);
  const labelOf = (key: string) => statusList.find((s) => s.key === key)?.label || key;
  const terminalKey = $derived(
    statusList.find((s) => s.terminal)?.key ?? statusList[statusList.length - 1]?.key ?? "",
  );
  const accents = [
    "bg-prog-100 text-prog-400",
    "bg-cau-100 text-cau-400",
    "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600",
    "bg-link-100 text-link-400",
  ];
  function pillFor(key: string): string {
    if (key === terminalKey) return "bg-pos-100 text-pos-400";
    const i = statusList.findIndex((s) => s.key === key);
    return i < 0 ? "bg-white-300 text-black-800" : accents[i % accents.length];
  }

  let busy = $state(false);

  /* ── create a ticket from this chat ── */
  let creating = $state(false);
  let newTitle = $state("");

  function startCreate() {
    creating = true;
    picking = false;
    // Prefill from the chat's own title: it already summarises what this is
    // about, so retyping it is busywork.
    newTitle = document.title.replace(/\s+[—|].*$/, "").trim();
  }

  function submitCreate() {
    const title = newTitle.trim();
    if (title === "" || !projectId) return;
    busy = true;
    Effect.runPromise(
      createTicket(base, projectId, { title, session_id: sessionId }).pipe(
        Effect.provide(WickClientLayer),
      ),
    )
      .then(() => {
        creating = false;
        newTitle = "";
        onChanged?.();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to create ticket"))
      .finally(() => { busy = false; });
  }

  /* ── attach to / move to an existing ticket ── */
  let picking = $state(false);
  let options = $state<TicketCard[]>([]);
  let loadingOptions = $state(false);
  /* A project collects dozens of open tickets, and scrolling a short list
     for one is the slow way — the picker is searched, by id or title. */
  let pickQuery = $state("");
  const shownOptions = $derived(rankTickets(options, pickQuery));

  function startPick() {
    picking = true;
    creating = false;
    pickQuery = "";
    if (!projectId) return;
    loadingOptions = true;
    // rows: 0 — the picker needs ids and titles, not each card's chat rows.
    Effect.runPromise(getProjectTickets(base, projectId, { rows: 0 }).pipe(Effect.provide(WickClientLayer)))
      .then((b) => {
        // Done tickets are not where live work goes, so they are left out of
        // the picker rather than padding a long list.
        options = b.tickets.filter((t) => t.status !== "done" && t.id !== ticket?.id);
      })
      .catch(() => { options = []; })
      .finally(() => { loadingOptions = false; });
  }

  function pick(ticketId: string) {
    busy = true;
    // Attach also detaches from the current ticket server-side, and the
    // chat's notes travel with it — so a move is one call.
    Effect.runPromise(attachSession(base, ticketId, sessionId).pipe(Effect.provide(WickClientLayer)))
      .then(() => {
        picking = false;
        options = [];
        onChanged?.();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to attach the chat"))
      .finally(() => { busy = false; });
  }

  function detach() {
    if (!ticket) return;
    busy = true;
    Effect.runPromise(
      detachSession(base, ticket.id, sessionId).pipe(Effect.provide(WickClientLayer)),
    )
      .then(() => onChanged?.())
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to detach"))
      .finally(() => { busy = false; });
  }

  /* ── edit the ticket's title in place ── */
  let editingTitle = $state(false);
  let titleDraft = $state("");

  /* Notes, in the ticket. They used to be a pointer to another tab, on the
     grounds that notes are not a ticket feature — true, and still true, but
     it made the one place you look at a ticket the one place you could not
     read what had been written about it. The section is here and open; the
     Notes tab remains the place notes live on a chat with no ticket. */
  let notesOpen = $state(true);

  /* The ticket's own description, folded past a few lines. It is the half of
     the story the notes answer, and it used to live only on the Notes tab —
     so with the two tabs merged it comes here. Folded because a long
     description would push the notes themselves off the panel, which is the
     opposite of helping. */
  const body = $derived((ticket?.body ?? "").trim());
  let bodyOpen = $state(false);
  const bodyLong = $derived(body.length > 240 || body.split("\n").length > 4);

  /* ── edit the description in place ──
     An empty description is an invitation, not an absence: the slot is
     shown like "Write a note" so the request can be written where it is
     read. Ctrl/Cmd+Enter saves, Escape leaves it untouched. */
  let editingBody = $state(false);
  let bodyDraft = $state("");
  let savingBody = $state(false);

  function startBody() {
    bodyDraft = body;
    editingBody = true;
  }

  function saveBody() {
    if (!ticket || savingBody) return;
    const next = bodyDraft.trim();
    if (next === body) {
      editingBody = false;
      return;
    }
    savingBody = true;
    Effect.runPromise(
      updateTicket(base, ticket.id, { body: next }).pipe(Effect.provide(WickClientLayer)),
    )
      .then(() => {
        editingBody = false;
        onChanged?.();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to save the description"))
      .finally(() => { savingBody = false; });
  }

  /* One field at a time: the server merges by key, so saving one never
     touches the others. */
  function saveField(key: string, value: string): Promise<void> {
    if (!ticket) return Promise.resolve();
    return Effect.runPromise(
      updateTicket(base, ticket.id, { fields: { [key]: value } }).pipe(Effect.provide(WickClientLayer)),
    ).then(() => { onChanged?.(); });
  }

  function focusOnMount(el: HTMLElement) {
    el.focus();
  }

  /* ── custom buttons ("Sync from Notion") ──
     Same behaviour as on the ticket's own page: one click at a time, the
     receiver's own words in the toast, and the ticket re-read twice after a
     success, because some receivers answer before their write has landed. */
  let actionBusy = $state("");
  function runButton(b: { id: string; label: string }) {
    if (!ticket || actionBusy !== "") return;
    actionBusy = b.id;
    Effect.runPromise(runTicketAction(base, ticket.id, b.id).pipe(Effect.provide(WickClientLayer)))
      .then((r) => {
        if (r.ok) {
          toastOk(`${b.label}: ${r.message || `done (HTTP ${r.status})`}`);
          onChanged?.();
          setTimeout(() => onChanged?.(), 2500);
        } else {
          toastError(`${b.label} failed: ${r.error || r.message || "HTTP " + r.status}`);
        }
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : `${b.label} failed`))
      .finally(() => { actionBusy = ""; });
  }
  const isSyncLabel = (label: string) => /sync|refresh|pull|reload/i.test(label);
  /* The header has room for a word, not a sentence: "Sync from Notion" reads
     as "Sync" beside the status pill, and the full label is the tooltip. */
  const shortLabel = (label: string) => (isSyncLabel(label) ? "Sync" : label.length > 14 ? label.slice(0, 13) + "…" : label);

  function startTitle() {
    if (!ticket) return;
    titleDraft = ticket.title;
    editingTitle = true;
  }

  function saveTitle() {
    editingTitle = false;
    const t = titleDraft.trim();
    if (!ticket || t === "" || t === ticket.title) return;
    Effect.runPromise(
      updateTicket(base, ticket.id, { title: t }).pipe(Effect.provide(WickClientLayer)),
    )
      .then(() => onChanged?.())
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to rename"));
  }

  function setStatus(status: string) {
    if (!ticket) return;
    Effect.runPromise(
      updateTicket(base, ticket.id, { status }).pipe(Effect.provide(WickClientLayer)),
    )
      .then(() => onChanged?.())
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to update status"));
  }
</script>

{#snippet pickList(emptyText: string)}
  {#if loadingOptions}
    <p class="text-[11px] text-black-700 dark:text-black-600">Loading tickets…</p>
  {:else if options.length === 0}
    <p class="text-[11px] text-black-700 dark:text-black-600">{emptyText}</p>
  {:else}
    <input
      use:focusOnMount
      bind:value={pickQuery}
      data-testid="ticket-pick-search"
      aria-label="Search tickets by id or title"
      placeholder="Search id or title…"
      onkeydown={(e) => {
        if (e.key === "Escape") { picking = false; }
        // Enter takes the top hit — an exact id pasted in is one keystroke.
        else if (e.key === "Enter" && shownOptions.length > 0 && !busy) { e.preventDefault(); pick(shownOptions[0].id); }
      }}
      class="mb-1.5 w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
    />
    {#if shownOptions.length === 0}
      <p class="text-[11px] text-black-700 dark:text-black-600">No ticket matches “{pickQuery.trim()}”.</p>
    {:else}
      <ul class="flex max-h-60 flex-col gap-1 overflow-y-auto" data-testid="ticket-pick-list">
        {#each shownOptions as t (t.id)}
          <li>
            <button
              type="button"
              disabled={busy}
              onclick={() => pick(t.id)}
              title={t.id + " — " + t.title}
              class="flex w-full items-center gap-2 rounded border border-white-300 bg-white-100 px-2 py-1.5 text-left text-[11px] transition-colors hover:border-green-500 disabled:opacity-40 dark:border-navy-600 dark:bg-navy-700"
            >
              <!-- Shortened: a 32-character id would otherwise take the whole
                   row and leave the title — the part people read — as "…". -->
              <span class="shrink-0 font-mono text-[10px] text-black-700 dark:text-black-600">{shortTicketId(t.id)}</span>
              <span class="min-w-0 flex-1 truncate text-black-900 dark:text-white-100">{t.title}</span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
{/snippet}

<div class="flex h-full flex-col overflow-y-auto p-4">
  <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Ticket</h3>

  {#if ticket}
    <div class="mt-2 rounded-lg border border-white-300 bg-white-200 p-3 dark:border-navy-600 dark:bg-navy-800">
      <div class="flex min-w-0 items-center gap-2">
        <!-- An adopted external id runs 32+ characters: shortened, so it
             never shoves the buttons and status pill off the header. The
             full id is the tooltip and the ticket's page. -->
        <button
          type="button"
          onclick={() => onOpenTicket?.(ticket.id)}
          title={"Open this ticket's page — " + ticket.id}
          class="min-w-0 truncate rounded bg-white-100 px-1.5 py-0.5 font-mono text-[10px] font-semibold text-black-800 transition-colors hover:text-green-600 dark:bg-navy-700 dark:text-black-600 dark:hover:text-green-400"
        >{shortTicketId(ticket.id)}</button>
        <span class="ml-auto flex shrink-0 items-center gap-1.5">
          {#if buttons && buttons.length > 0}
            <!-- Custom buttons ("Sync from Notion") sit in the header, quiet
                 until hovered: an action on the ticket, not content of it. -->
            <span class="flex items-center gap-0.5" data-testid="ticket-buttons">
              {#each buttons as b (b.id)}
                <button
                  type="button"
                  data-testid="ticket-button-{b.id}"
                  disabled={actionBusy !== ""}
                  aria-busy={actionBusy === b.id}
                  onclick={() => runButton(b)}
                  title={b.label}
                  aria-label={b.label}
                  class="inline-flex h-6 items-center gap-1 rounded-md px-1.5 text-[11px] font-medium text-black-700 transition-colors hover:bg-white-100 hover:text-green-600 disabled:cursor-not-allowed disabled:opacity-60 dark:text-black-600 dark:hover:bg-navy-700 dark:hover:text-green-400"
                >
                  {#if isSyncLabel(b.label)}
                    <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 {actionBusy === b.id ? 'animate-spin' : ''}" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                      <path d="M13 8a5 5 0 0 1-8.6 3.5M3 8a5 5 0 0 1 8.6-3.5M11.5 2v2.7H8.8M4.5 14v-2.7h2.7" stroke-linecap="round" stroke-linejoin="round"></path>
                    </svg>
                  {:else if actionBusy === b.id}
                    <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 animate-spin" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
                      <path d="M8 2a6 6 0 1 1-6 6" stroke-linecap="round"></path>
                    </svg>
                  {/if}
                  <span>{actionBusy === b.id ? (isSyncLabel(b.label) ? "Syncing…" : "Working…") : shortLabel(b.label)}</span>
                </button>
              {/each}
            </span>
          {/if}
          <span class={"rounded-full px-2 py-0.5 text-[10px] font-semibold " + pillFor(ticket.status)}>
            {labelOf(ticket.status)}
          </span>
        </span>
      </div>

      {#if editingTitle}
        <input
          bind:value={titleDraft}
          onblur={saveTitle}
          onkeydown={(e) => { if (e.key === "Enter") saveTitle(); }}
          aria-label="Ticket title"
          class="mt-2 w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
        />
      {:else}
        <button
          type="button"
          onclick={startTitle}
          title="Rename this ticket"
          class="mt-2 w-full rounded text-left text-xs font-medium text-black-900 hover:bg-white-100 dark:text-white-100 dark:hover:bg-navy-700"
        >{ticket.title}</button>
      {/if}


      <label class="mt-3 block text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600" for="rail-tkt-status">
        Status
      </label>
      <select
        id="rail-tkt-status"
        value={ticket.status}
        onchange={(e) => setStatus((e.target as HTMLSelectElement).value)}
        class="mt-1 w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
      >
        {#each statusList as s (s.key)}
          <option value={s.key}>{s.label || s.key}</option>
        {/each}
      </select>

      {#if editingBody}
        <section
          data-testid="ticket-body-editor"
          class="mt-3 rounded-lg border border-white-300 bg-white-100 p-2.5 dark:border-navy-600 dark:bg-navy-700"
        >
          <h4 class="text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">
            What the ticket asks
          </h4>
          <textarea
            use:focusOnMount
            bind:value={bodyDraft}
            rows="6"
            disabled={savingBody}
            data-testid="ticket-body-input"
            aria-label="What the ticket asks"
            placeholder="What is being asked, links, repro steps… (markdown)"
            onkeydown={(e) => {
              if (e.key === "Escape") { editingBody = false; }
              else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); saveBody(); }
            }}
            class="mt-1.5 w-full resize-y rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          ></textarea>
          <div class="mt-1.5 flex items-center gap-2">
            <button
              type="button"
              data-testid="ticket-body-save"
              disabled={savingBody}
              onclick={saveBody}
              class="rounded-lg bg-green-600 px-2.5 py-1 text-[11px] font-medium text-white-100 transition-colors hover:bg-green-700 disabled:opacity-40"
            >{savingBody ? "Saving…" : "Save"}</button>
            <button
              type="button"
              disabled={savingBody}
              onclick={() => { editingBody = false; }}
              class="text-[11px] text-black-700 hover:underline disabled:opacity-40 dark:text-black-600"
            >Cancel</button>
          </div>
        </section>
      {:else if body}
        <!-- What the ticket ASKS, above the record of what was found. -->
        <section
          data-testid="ticket-body"
          class="mt-3 rounded-lg border border-white-300 bg-white-100 p-2.5 dark:border-navy-600 dark:bg-navy-700"
        >
          <div class="flex items-center justify-between gap-2">
            <h4 class="text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">
              What the ticket asks
            </h4>
            <button
              type="button"
              data-testid="ticket-body-edit"
              onclick={startBody}
              title="Edit what the ticket asks"
              class="ml-auto shrink-0 text-[11px] font-medium text-black-700 hover:text-green-600 hover:underline dark:text-black-600 dark:hover:text-green-400"
            >Edit</button>
          </div>
          <div class="relative">
            <!-- Clicking the folded text is a mouse shortcut to "Show more";
                 the keyboard path is the toggle button below, which is a real
                 focusable button — so the text itself stays a plain block. -->
            <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
            <div
              class="wick-note-md mt-1.5 break-words text-xs text-black-900 dark:text-white-100 {bodyLong && !bodyOpen ? 'max-h-24 cursor-pointer overflow-hidden' : ''}"
              data-testid="ticket-body-text"
              onclick={(e) => {
                // Folded text opens on a click anywhere in it — except on a
                // link, which must still go where it points.
                if (bodyLong && !bodyOpen && !(e.target as HTMLElement).closest("a")) bodyOpen = true;
              }}
            >
              {@html renderMarkdown(body)}
            </div>
            {#if bodyLong && !bodyOpen}
              <div
                aria-hidden="true"
                class="pointer-events-none absolute inset-x-0 bottom-0 h-6 bg-gradient-to-t from-white-100 to-transparent dark:from-navy-700"
              ></div>
            {/if}
          </div>
          {#if bodyLong}
            <FoldToggle open={bodyOpen} testid="ticket-body-toggle" onclick={() => { bodyOpen = !bodyOpen; }} />
          {/if}
        </section>
      {:else}
        <button
          type="button"
          data-testid="ticket-body-add"
          onclick={startBody}
          class="mt-3 flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-white-400 px-3 py-2 text-xs font-medium text-black-700 transition-colors hover:border-green-500 hover:text-green-600 dark:border-navy-600 dark:text-black-600 dark:hover:text-green-400"
        >
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
            <path d="M8 3.5v9M3.5 8h9" stroke-linecap="round"></path>
          </svg>
          Describe what the ticket asks
        </button>
      {/if}

      {#if fields && fields.length > 0}
        <section
          data-testid="ticket-fields"
          class="mt-3 rounded-lg border border-white-300 bg-white-100 p-2.5 dark:border-navy-600 dark:bg-navy-700"
        >
          <h4 class="text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">
            Custom fields
          </h4>
          <div class="mt-1.5">
            <TicketFields {fields} values={ticket.fields} onSave={saveField} />
          </div>
        </section>
      {/if}

      {#if picking}
        <div class="mt-3">
          {@render pickList("No other open ticket to move to.")}
          <button
            type="button"
            onclick={() => { picking = false; }}
            class="mt-1 text-[11px] text-black-700 hover:underline dark:text-black-600"
          >Cancel</button>
        </div>
      {:else}
        <div class="mt-3 flex flex-wrap gap-2">
          <button
            type="button"
            onclick={startPick}
            class="text-[11px] font-medium text-green-600 hover:underline dark:text-green-400"
          >Move to another ticket…</button>
          <button
            type="button"
            disabled={busy}
            onclick={detach}
            title="Keep the chat, take it off this ticket"
            class="text-[11px] text-black-700 hover:underline disabled:opacity-40 dark:text-black-600"
          >Detach</button>
        </div>
      {/if}
    </div>
  {:else}
    <div class="mt-2 rounded-lg border border-dashed border-white-400 bg-white-200 p-3 dark:border-navy-600 dark:bg-navy-800">
      <p class="text-[11px] leading-relaxed text-black-700 dark:text-black-600">
        This chat belongs to no ticket. Put it on one to track it on the board and let other
        sessions continue the same work.
      </p>

      {#if !projectId}
        <p class="mt-2 text-[11px] text-black-600 dark:text-black-700">
          A chat outside a project cannot hold a ticket.
        </p>
      {:else if creating}
        <form onsubmit={(e) => { e.preventDefault(); submitCreate(); }} class="mt-2">
          <input
            bind:value={newTitle}
            placeholder="What is this work?"
            aria-label="New ticket title"
            class="w-full rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
          />
          <div class="mt-2 flex gap-2">
            <button
              type="submit"
              disabled={busy || newTitle.trim() === ""}
              class="rounded-lg bg-green-600 px-3 py-1 text-[11px] font-semibold text-white-100 transition-colors hover:bg-green-700 disabled:opacity-40"
            >{busy ? "Creating…" : "Create ticket"}</button>
            <button
              type="button"
              onclick={() => { creating = false; }}
              class="rounded-lg px-2 py-1 text-[11px] text-black-700 hover:bg-white-100 dark:text-black-600 dark:hover:bg-navy-700"
            >Cancel</button>
          </div>
        </form>
      {:else if picking}
        <div class="mt-2">
          {@render pickList("No open ticket yet — create one.")}
          <button
            type="button"
            onclick={() => { picking = false; }}
            class="mt-1 text-[11px] text-black-700 hover:underline dark:text-black-600"
          >Cancel</button>
        </div>
      {:else}
        <div class="mt-2 flex flex-wrap gap-2">
          <button
            type="button"
            onclick={startCreate}
            class="rounded-lg bg-green-600 px-3 py-1 text-[11px] font-semibold text-white-100 transition-colors hover:bg-green-700"
          >Create ticket from this chat</button>
          <button
            type="button"
            onclick={startPick}
            class="rounded-lg border border-white-400 px-3 py-1 text-[11px] font-medium text-black-800 transition-colors hover:bg-white-100 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
          >Attach to existing…</button>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Notes, in place. The header still points at the Notes tab, which is
       where the same notes live for a chat that is on no ticket at all. -->
  <section class="mt-3 border-t border-white-300 pt-3 dark:border-navy-600">
    <div class="flex items-center gap-2">
      <button
        type="button"
        data-testid="ticket-notes-toggle"
        aria-expanded={notesOpen}
        onclick={() => { notesOpen = !notesOpen; }}
        class="flex min-w-0 flex-1 items-center gap-2 text-left text-[11px] font-semibold uppercase tracking-wide text-black-700 transition-colors hover:text-green-600 dark:text-black-600 dark:hover:text-green-400"
      >
        <svg
          viewBox="0 0 16 16"
          class="h-3 w-3 shrink-0 transition-transform {notesOpen ? 'rotate-90' : ''}"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          aria-hidden="true"
        >
          <path d="M6 3.5L10.5 8 6 12.5" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
        <span class="truncate">Notes on {ticket ? "this ticket" : "this chat"}</span>
        {#if noteCount > 0}
          <span
            class="inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-green-500 px-1 text-[10px] font-semibold text-white-100"
          >{noteCount > 99 ? "99+" : noteCount}</span>
        {/if}
      </button>
      {#if onOpenNotes}
        <button
          type="button"
          onclick={() => onOpenNotes?.()}
          title="Open the Notes tab"
          class="shrink-0 text-[11px] font-medium text-green-600 hover:underline dark:text-green-400"
        >Open tab →</button>
      {/if}
    </div>

    {#if notesOpen}
      <div class="mt-2">
        <NotesPanel {base} scope={{ sessionId }} {notes} {users} {onChanged} />
      </div>
    {/if}
  </section>
</div>
