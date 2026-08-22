<script lang="ts">
  /* The rail's Notes tab.

     Notes are their own thing, not a ticket feature — they work on a chat
     that belongs to no ticket at all. What a ticket changes is only the
     SCOPE: when this chat is on one, the notes below are the ticket's and
     every session on it reads the same set; otherwise they are this chat's
     alone. So this panel says which scope is in effect and gets out of the
     way. Everything about the ticket itself lives in the Ticket tab. */
  import NotesPanel from "./NotesPanel.svelte";
  import type { NotesResponse } from "../types/agents.js";

  type Props = {
    base: string;
    sessionId: string;
    /* Set when the resolved scope is a ticket's. */
    ticket?: { id: string; title: string; status: string } | null;
    info?: NotesResponse | null;
    onChanged?: () => void;
  };

  let { base, sessionId, ticket, info, onChanged }: Props = $props();
</script>

<div class="flex h-full flex-col overflow-y-auto p-4">
  <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Notes</h3>

  <!-- One line on scope, because it decides who else will read this. -->
  {#if ticket}
    <p class="mt-1 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
      Shared across every session on
      <span class="rounded bg-white-200 px-1 font-mono text-[10px] text-black-800 dark:bg-navy-800 dark:text-black-600">{ticket.id}</span>
      — what you write here is what the next one reads.
    </p>
  {:else}
    <p class="mt-1 text-[11px] leading-relaxed text-black-700 dark:text-black-600">
      Private to this chat. Put it on a ticket (Ticket tab) and these travel along, shared with
      every session there.
    </p>
  {/if}

  <div class="mt-3">
    <NotesPanel
      {base}
      scope={{ sessionId }}
      notes={info?.notes}
      users={info?.users}
      {onChanged}
    />
  </div>
</div>
