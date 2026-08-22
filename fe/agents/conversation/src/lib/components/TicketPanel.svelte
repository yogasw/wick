<script lang="ts">
  /* The rail's Ticket tab — about the ticket only. Notes live in their own
     tab, because notes are not a ticket feature: they work on a chat with no
     ticket at all, and a ticket only changes whose notes these are.

     Two states:
     - ON a ticket: shows it, lets the title be fixed, and lets this chat be
       moved to a different ticket or taken off tickets entirely.
     - On nothing: offers to create a ticket from this chat, or attach it to
       an existing one. */
  import type { TicketCard } from "../types/agents.js";
  import {
    attachSession,
    createTicket,
    detachSession,
    getProjectTickets,
    updateTicket,
  } from "../api/tickets.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";

  type Props = {
    base: string;
    sessionId: string;
    /* Project the session belongs to; needed to create or list tickets. */
    projectId?: string;
    /* Ticket this session is attached to, when any. */
    ticket?: { id: string; title: string; status: string } | null;
    /* How many notes the resolved scope holds — shown as a pointer to the
       Notes tab rather than duplicating the list here. */
    noteCount?: number;
    /* Opens the ticket's own page (sessions, fields, full note list). */
    onOpenTicket?: (ticketId: string) => void;
    /* Switches the rail to the Notes tab. */
    onOpenNotes?: () => void;
    onChanged?: () => void;
  };

  let {
    base,
    sessionId,
    projectId,
    ticket,
    noteCount = 0,
    onOpenTicket,
    onOpenNotes,
    onChanged,
  }: Props = $props();

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

  function startPick() {
    picking = true;
    creating = false;
    if (!projectId) return;
    loadingOptions = true;
    Effect.runPromise(getProjectTickets(base, projectId).pipe(Effect.provide(WickClientLayer)))
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

<div class="flex h-full flex-col overflow-y-auto p-4">
  <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Ticket</h3>

  {#if ticket}
    <div class="mt-2 rounded-lg border border-white-300 bg-white-200 p-3 dark:border-navy-600 dark:bg-navy-800">
      <div class="flex items-center gap-2">
        <button
          type="button"
          onclick={() => onOpenTicket?.(ticket.id)}
          title="Open this ticket's page"
          class="rounded bg-white-100 px-1.5 py-0.5 font-mono text-[10px] font-semibold text-black-800 transition-colors hover:text-green-600 dark:bg-navy-700 dark:text-black-600 dark:hover:text-green-400"
        >{ticket.id}</button>
        <span class={"ml-auto rounded-full px-2 py-0.5 text-[10px] font-semibold " + (statusPill[ticket.status] ?? "")}>
          {statusLabels[ticket.status] ?? ticket.status}
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
        {#each Object.keys(statusLabels) as s (s)}
          <option value={s}>{statusLabels[s]}</option>
        {/each}
      </select>

      {#if picking}
        <div class="mt-3">
          {#if loadingOptions}
            <p class="text-[11px] text-black-700 dark:text-black-600">Loading tickets…</p>
          {:else if options.length === 0}
            <p class="text-[11px] text-black-700 dark:text-black-600">No other open ticket to move to.</p>
          {:else}
            <ul class="flex max-h-40 flex-col gap-1 overflow-y-auto">
              {#each options as t (t.id)}
                <li>
                  <button
                    type="button"
                    disabled={busy}
                    onclick={() => pick(t.id)}
                    class="flex w-full items-center gap-2 rounded border border-white-300 bg-white-100 px-2 py-1.5 text-left text-[11px] transition-colors hover:border-green-500 disabled:opacity-40 dark:border-navy-600 dark:bg-navy-700"
                  >
                    <span class="shrink-0 font-mono text-[10px] text-black-700 dark:text-black-600">{t.id}</span>
                    <span class="min-w-0 flex-1 truncate text-black-900 dark:text-white-100">{t.title}</span>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
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
          {#if loadingOptions}
            <p class="text-[11px] text-black-700 dark:text-black-600">Loading tickets…</p>
          {:else if options.length === 0}
            <p class="text-[11px] text-black-700 dark:text-black-600">No open ticket yet — create one.</p>
          {:else}
            <ul class="flex max-h-40 flex-col gap-1 overflow-y-auto">
              {#each options as t (t.id)}
                <li>
                  <button
                    type="button"
                    disabled={busy}
                    onclick={() => pick(t.id)}
                    class="flex w-full items-center gap-2 rounded border border-white-300 bg-white-100 px-2 py-1.5 text-left text-[11px] transition-colors hover:border-green-500 disabled:opacity-40 dark:border-navy-600 dark:bg-navy-700"
                  >
                    <span class="shrink-0 font-mono text-[10px] text-black-700 dark:text-black-600">{t.id}</span>
                    <span class="min-w-0 flex-1 truncate text-black-900 dark:text-white-100">{t.title}</span>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
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

  <!-- A pointer, not a copy: the notes themselves are one tab away, and
       duplicating them here is what made the two look like one feature. -->
  <button
    type="button"
    onclick={() => onOpenNotes?.()}
    class="mt-3 flex items-center gap-2 rounded-lg border border-white-300 px-3 py-2 text-left text-[11px] text-black-700 transition-colors hover:border-green-500 hover:text-green-600 dark:border-navy-600 dark:text-black-600 dark:hover:text-green-400"
  >
    <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
      <path d="M4 2.5h8v11H4z" stroke-linejoin="round"></path>
      <path d="M6 5.5h4M6 8h4M6 10.5h2.5" stroke-linecap="round"></path>
    </svg>
    {#if noteCount > 0}
      {noteCount} note{noteCount === 1 ? "" : "s"} on {ticket ? "this ticket" : "this chat"} →
    {:else}
      No notes yet →
    {/if}
  </button>
</div>
