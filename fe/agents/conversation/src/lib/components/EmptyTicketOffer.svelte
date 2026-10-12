<script lang="ts">
  /* Offers to delete a ticket that just lost its last chat — the project
     board's and Team › Tickets' combined board's. The host calls offer()
     with the ticket the move emptied; the standing answer ("don't ask
     again") is the user's ticket preference, read here. */
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { deleteTicket, getTicketPrefs, saveTicketPrefs, type EmptiedTicket } from "../api/tickets.js";

  type Props = {
    base: string;
    /* The board needs a re-read: after a delete, or after "Keep it".
       Names the project the ticket is in when offer() was told it. */
    onChanged: (projectId?: string) => void;
  };
  let { base, onChanged }: Props = $props();

  /* The project of the ticket on offer (multi-project boards). */
  let emptiedProject: string | undefined;
  const reloadBoard = () => onChanged(emptiedProject);

  /* ── the offer ──
     A ticket with no sessions tracks nothing, so removal is offered. The
     answer can be made standing ("don't ask again"), which is safe here
     precisely because deleting an EMPTY ticket destroys no conversation —
     unlike deleting one that still holds chats, which always asks. */
  let emptied = $state<EmptiedTicket | null>(null);
  let dontAskEmpty = $state(false);
  let autoDeleteEmpty = $state<"" | "always" | "never">("");

  $effect(() => {
    Effect.runPromise(getTicketPrefs(base).pipe(Effect.provide(WickClientLayer)))
      .then((p) => { autoDeleteEmpty = p.auto_delete_empty ?? ""; })
      .catch(() => { /* asking is the safe default */ });
  });

  export function offer(t: EmptiedTicket, projectId?: string) {
    emptiedProject = projectId;
    if (autoDeleteEmpty === "never") return;
    if (autoDeleteEmpty === "always") {
      removeEmptyTicket(t.id);
      return;
    }
    emptied = t;
    dontAskEmpty = false;
  }

  function removeEmptyTicket(ticketId: string, projectId = emptiedProject) {
    // "keep": an empty ticket has no chats to take with it, so this is only
    // ever the ticket record.
    Effect.runPromise(deleteTicket(base, ticketId, "keep", projectId).pipe(Effect.provide(WickClientLayer)))
      .then(() => onChanged(projectId))
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to delete the ticket"));
  }

  function answerEmptied(remove: boolean) {
    const t = emptied;
    emptied = null;
    if (dontAskEmpty) {
      const choice = remove ? "always" : "never";
      autoDeleteEmpty = choice;
      Effect.runPromise(
        saveTicketPrefs(base, { auto_delete_empty: choice }).pipe(Effect.provide(WickClientLayer)),
      ).catch(() => { /* the prompt will simply appear again */ });
    }
    if (remove && t) removeEmptyTicket(t.id);
    else reloadBoard();
  }
</script>

<!-- The last chat left a ticket, so the ticket now tracks nothing. Offering
     removal here (rather than doing it silently) keeps a deliberately-empty
     ticket — one just created, not yet used — from disappearing. -->
{#if emptied}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-navy-900/40 p-4">
    <div class="w-full max-w-sm rounded-xl border border-white-300 bg-white-100 p-5 shadow-xl dark:border-navy-600 dark:bg-navy-700">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">
        Delete the empty ticket?
      </h2>
      <p class="mt-2 text-xs leading-relaxed text-black-700 dark:text-black-600">
        <span class="rounded bg-white-200 px-1 font-mono text-[10px] dark:bg-navy-800">{emptied.id}</span>
        “{emptied.title}” has no chats left. Nothing else is deleted — the chat you moved is
        safe on its new ticket.
      </p>

      <label class="mt-4 flex cursor-pointer items-start gap-2 text-xs text-black-800 dark:text-black-600">
        <input
          type="checkbox"
          bind:checked={dontAskEmpty}
          class="mt-0.5 h-3.5 w-3.5 rounded border-white-400 text-green-600 focus:ring-green-500 dark:border-navy-600"
        />
        <span>
          Don’t ask again — remember my answer.
          <span class="mt-0.5 block text-[11px] text-black-700 dark:text-black-600">
            Only for empty tickets. Deleting a ticket that still holds chats always asks.
          </span>
        </span>
      </label>

      <div class="mt-4 flex justify-end gap-2">
        <button
          type="button"
          onclick={() => answerEmptied(false)}
          class="rounded-lg border border-white-400 px-3 py-1.5 text-xs text-black-800 transition-colors hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-800"
        >Keep it</button>
        <button
          type="button"
          onclick={() => answerEmptied(true)}
          class="rounded-lg bg-neg-400 px-3 py-1.5 text-xs font-semibold text-white-100 transition-colors hover:bg-neg-500"
        >Delete ticket</button>
      </div>
    </div>
  </div>
{/if}
