<script lang="ts">
  /* Notes on a ticket or a session.

     Notes are the running record of what has been learned, so the list is
     chronological and edits are inline — the point is to lower the cost of
     writing one down, not to build a document editor.

     Two controls carry meaning that is easy to get wrong:
     - AUDIENCE says who a note was written for (robot / person / both). It
       is a label, not a permission: the agent reads every audience.
     - HIDE is the permission. A hidden note never reaches the agent; it
       stays in this list, blurred, and can be un-hidden. Hiding is not
       deleting. */
  import type { Note } from "../types/agents.js";
  import type { NotesScope } from "../api/tickets.js";
  import { addNote, deleteNote, listNotes, updateNote } from "../api/tickets.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { timeAgo } from "../timeFormat.js";

  type Props = {
    base: string;
    scope: NotesScope;
    /* Seed list, so a parent that already fetched does not re-fetch. */
    notes?: Note[];
    users?: Record<string, string>;
    /* Called after any change, for parents that show a note count. */
    onChanged?: () => void;
  };

  let { base, scope, notes: seed, users, onChanged }: Props = $props();

  let items = $state<Note[]>([]);
  let loaded = $state(false);
  let draft = $state("");
  let draftCheckable = $state(false);
  let draftAudience = $state<"both" | "ai" | "human">("both");
  let busy = $state(false);
  let editingId = $state<string | null>(null);
  let editBody = $state("");

  const run = <T,>(e: Effect.Effect<T, unknown, never>) => Effect.runPromise(e as never);

  function reload() {
    Effect.runPromise(listNotes(base, scope).pipe(Effect.provide(WickClientLayer)))
      .then((r) => {
        items = r.notes;
        loaded = true;
      })
      .catch(() => {
        loaded = true;
      });
    onChanged?.();
  }

  $effect(() => {
    if (seed && !loaded) {
      items = seed;
      loaded = true;
      return;
    }
    if (!loaded) reload();
  });

  const audienceLabel: Record<string, string> = {
    ai: "for the agent",
    human: "for people",
    both: "for anyone",
  };

  function add() {
    const body = draft.trim();
    if (body === "" || busy) return;
    busy = true;
    Effect.runPromise(
      addNote(base, scope, {
        body,
        checkable: draftCheckable,
        audience: draftAudience,
      }).pipe(Effect.provide(WickClientLayer)),
    )
      .then((n) => {
        items = [...items, n];
        draft = "";
        draftCheckable = false;
        onChanged?.();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to add note"))
      .finally(() => {
        busy = false;
      });
  }

  function patch(note: Note, body: Parameters<typeof updateNote>[3]) {
    Effect.runPromise(updateNote(base, scope, note.id, body).pipe(Effect.provide(WickClientLayer)))
      .then((updated) => {
        items = items.map((n) => (n.id === updated.id ? updated : n));
        onChanged?.();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to update note"));
  }

  function remove(note: Note) {
    Effect.runPromise(deleteNote(base, scope, note.id).pipe(Effect.provide(WickClientLayer)))
      .then(() => {
        items = items.filter((n) => n.id !== note.id);
        onChanged?.();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to delete note"));
  }

  function saveEdit(note: Note) {
    const body = editBody.trim();
    editingId = null;
    if (body === "" || body === note.body) return;
    patch(note, { body });
  }

  function authorName(n: Note): string {
    if (n.author === "agent") return "agent";
    return n.author ? (users?.[n.author] ?? "someone") : "";
  }
</script>

<div class="flex flex-col gap-3">
  <!-- Compose. Kept at the top so writing a note is the first thing
       available, not something to scroll past a long list for. -->
  <div class="rounded-lg border border-white-300 bg-white-100 p-3 dark:border-navy-600 dark:bg-navy-700">
    <textarea
      bind:value={draft}
      rows="2"
      placeholder="What should the next session know?"
      aria-label="New note"
      class="w-full resize-y rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
    ></textarea>
    <div class="mt-2 flex flex-wrap items-center gap-2">
      <label class="flex cursor-pointer items-center gap-1.5 text-xs text-black-800 dark:text-black-600">
        <input
          type="checkbox"
          bind:checked={draftCheckable}
          class="h-3.5 w-3.5 rounded border-white-400 text-green-600 focus:ring-green-500 dark:border-navy-600"
        />
        Checkable
      </label>
      <select
        bind:value={draftAudience}
        aria-label="Note audience"
        class="rounded-lg border border-white-400 bg-white-100 px-2 py-1 text-xs text-black-800 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
      >
        <option value="both">For anyone</option>
        <option value="ai">For the agent</option>
        <option value="human">For people</option>
      </select>
      <button
        type="button"
        onclick={add}
        disabled={busy || draft.trim() === ""}
        class="ml-auto rounded-lg bg-green-600 px-3 py-1.5 text-xs font-semibold text-white-100 transition-colors hover:bg-green-700 disabled:opacity-40"
      >Add note</button>
    </div>
  </div>

  {#if !loaded}
    <p class="py-4 text-center text-xs text-black-700 dark:text-black-600">Loading…</p>
  {:else if items.length === 0}
    <p class="py-4 text-center text-xs text-black-700 dark:text-black-600">
      No notes yet. The first one is usually "here is what I found".
    </p>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each items as n (n.id)}
        <li
          data-testid={"note-" + n.id}
          class={[
            "rounded-lg border p-3 transition-colors",
            n.hidden
              ? "border-dashed border-white-400 bg-white-200 dark:border-navy-600 dark:bg-navy-800"
              : "border-white-300 bg-white-100 dark:border-navy-600 dark:bg-navy-700",
          ].join(" ")}
        >
          <div class="flex items-start gap-2">
            {#if n.checkable}
              <input
                type="checkbox"
                checked={n.done === true}
                aria-label="Mark note done"
                onchange={(e) => patch(n, { done: (e.target as HTMLInputElement).checked })}
                class="mt-0.5 h-4 w-4 shrink-0 rounded border-white-400 text-green-600 focus:ring-green-500 dark:border-navy-600"
              />
            {/if}

            <div class="min-w-0 flex-1">
              {#if editingId === n.id}
                <textarea
                  bind:value={editBody}
                  rows="3"
                  aria-label="Edit note"
                  class="w-full resize-y rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-sm text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
                ></textarea>
                <div class="mt-2 flex gap-2">
                  <button
                    type="button"
                    onclick={() => saveEdit(n)}
                    class="rounded-lg bg-green-600 px-3 py-1 text-xs font-semibold text-white-100 hover:bg-green-700"
                  >Save</button>
                  <button
                    type="button"
                    onclick={() => { editingId = null; }}
                    class="rounded-lg px-2 py-1 text-xs text-black-700 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800"
                  >Cancel</button>
                </div>
              {:else}
                <!-- Hidden notes are blurred until hovered: still here, but
                     clearly out of the agent's reach. -->
                <p
                  class={[
                    "whitespace-pre-wrap text-sm text-black-900 transition dark:text-white-100",
                    n.done ? "line-through opacity-60" : "",
                    n.hidden ? "blur-[3px] hover:blur-none" : "",
                  ].join(" ")}
                >{n.body}</p>
              {/if}

              <div class="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-black-700 dark:text-black-600">
                <span class="rounded bg-white-200 px-1.5 py-0.5 dark:bg-navy-800">
                  {audienceLabel[n.audience] ?? n.audience}
                </span>
                {#if n.hidden}
                  <span class="rounded bg-white-300 px-1.5 py-0.5 font-medium dark:bg-navy-600">hidden from agent</span>
                {/if}
                {#if authorName(n)}<span>{authorName(n)}</span>{/if}
                <span>{timeAgo(n.updated_at)}</span>
              </div>
            </div>

            <div class="flex shrink-0 items-center gap-1">
              <button
                type="button"
                aria-label={n.hidden ? "Show to agent" : "Hide from agent"}
                title={n.hidden ? "Show to agent" : "Hide from agent"}
                onclick={() => patch(n, { hidden: !n.hidden })}
                class="flex h-7 w-7 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800"
              >
                {#if n.hidden}
                  <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                    <path d="M2 8s2.5-4 6-4 6 4 6 4-2.5 4-6 4-6-4-6-4z" stroke-linejoin="round"></path>
                    <path d="M3 3l10 10" stroke-linecap="round"></path>
                  </svg>
                {:else}
                  <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                    <path d="M2 8s2.5-4 6-4 6 4 6 4-2.5 4-6 4-6-4-6-4z" stroke-linejoin="round"></path>
                    <circle cx="8" cy="8" r="1.5"></circle>
                  </svg>
                {/if}
              </button>
              <button
                type="button"
                aria-label="Edit note"
                onclick={() => { editingId = n.id; editBody = n.body; }}
                class="flex h-7 w-7 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                  <path d="M11 2.5l2.5 2.5L6 12.5H3.5V10L11 2.5z" stroke-linejoin="round"></path>
                </svg>
              </button>
              <button
                type="button"
                aria-label="Delete note"
                onclick={() => remove(n)}
                class="flex h-7 w-7 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                  <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
                </svg>
              </button>
            </div>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>
