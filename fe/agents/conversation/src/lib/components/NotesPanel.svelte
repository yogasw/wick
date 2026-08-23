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
       deleting.

     Both used to sit in a row of bare icons next to a "×", which read as
     "close" and deleted the note instead. Editing and deleting now live
     behind a named menu, and only the everyday toggle stays on the row. */
  import type { Note } from "../types/agents.js";
  import type { NotesScope } from "../api/tickets.js";
  import { addNote, deleteNote, listNotes, updateNote } from "../api/tickets.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { timeAgo } from "../timeFormat.js";
  import { renderMarkdown } from "../markdown.js";
  /* Imported here rather than relied on from elsewhere: this panel renders in
     the rail and inside a ticket, and neither should have to pull in the
     conversation's own stylesheet for a note to look right. */
  import "../notesMarkdown.css";

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
  /* Which note's "more" menu is open, and which one has been asked about
     before deleting. A note is someone's written record — losing it to a
     misread icon is what this second step exists to prevent. */
  let menuId = $state<string | null>(null);
  let confirmId = $state<string | null>(null);

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
    menuId = null;
    confirmId = null;
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

  /* Ctrl/Cmd+Enter saves and Escape abandons, in both the composer and an
     edit. A plain Enter has to stay a newline: these are notes, and the
     multi-line ones are the useful ones. */
  function composeKeys(e: KeyboardEvent) {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      add();
    }
  }

  function editKeys(e: KeyboardEvent, note: Note) {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      saveEdit(note);
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      editingId = null;
    }
  }

  function startEdit(note: Note) {
    menuId = null;
    confirmId = null;
    editingId = note.id;
    editBody = note.body;
  }

  function closeMenus() {
    menuId = null;
    confirmId = null;
  }

  // Notes store a USER ID so a rename shows up on old notes. Two sentinels are
  // not ids and must not be looked up:
  //   "unknown" — no human behind the call (cron, system job, legacy session)
  //   "agent"   — legacy value written before notes recorded the real caller
  // Both render as "unknown user": claiming an actor we cannot name is worse
  // than saying we cannot name one.
  function authorName(n: Note): string {
    if (!n.author) return "";
    if (n.author === "unknown" || n.author === "agent") return "unknown user";
    return users?.[n.author] ?? "unknown user";
  }
</script>

<div class="flex flex-col gap-3">
  <!-- Compose. Kept at the top so writing a note is the first thing
       available, not something to scroll past a long list for. -->
  <div class="rounded-lg border border-white-300 bg-white-100 p-3 dark:border-navy-600 dark:bg-navy-700">
    <textarea
      bind:value={draft}
      onkeydown={composeKeys}
      rows="3"
      placeholder="What should the next session know?"
      aria-label="New note"
      class="w-full resize-y rounded-lg border border-white-400 bg-white-100 px-3 py-2 text-sm leading-relaxed text-black-900 outline-none transition-colors focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
    ></textarea>
    <!-- Said once, above the controls: notes render as Markdown, and the
         list below shows them rendered rather than as the source typed. -->
    <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600">
      Markdown — <code class="rounded bg-white-200 px-1 font-mono dark:bg-navy-800">**bold**</code>,
      <code class="rounded bg-white-200 px-1 font-mono dark:bg-navy-800">`code`</code>, lists and
      links all render. <kbd class="font-sans">Ctrl</kbd>+<kbd class="font-sans">Enter</kbd> to add.
    </p>
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
                <!-- An edit shows the SOURCE, monospaced and roomy: what is
                     being typed is Markdown, and the rendered version is one
                     Save away. -->
                <textarea
                  bind:value={editBody}
                  onkeydown={(e) => editKeys(e, n)}
                  rows="6"
                  aria-label="Edit note"
                  class="w-full resize-y rounded-lg border border-green-500 bg-white-100 px-3 py-2 font-mono text-[13px] leading-relaxed text-black-900 outline-none ring-1 ring-green-500/30 dark:bg-navy-800 dark:text-white-100"
                ></textarea>
                <div class="mt-2 flex flex-wrap items-center gap-2">
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
                  <span class="text-[10px] text-black-700 dark:text-black-600">
                    Markdown · Ctrl+Enter saves, Esc cancels
                  </span>
                </div>
              {:else}
                <!-- Hidden notes are blurred until hovered: still here, but
                     clearly out of the agent's reach. -->
                <div
                  data-testid={"note-body-" + n.id}
                  class={[
                    "wick-note-md break-words text-black-900 transition dark:text-white-100",
                    n.done ? "line-through opacity-60" : "",
                    n.hidden ? "blur-[3px] hover:blur-none" : "",
                  ].join(" ")}
                >{@html renderMarkdown(n.body)}</div>
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
              <!-- Everything destructive or rare is behind "More". A bare ×
                   beside an edit pencil reads as "close this", which is the
                   one thing it did not do. -->
              <div class="relative">
                <button
                  type="button"
                  aria-label="More actions"
                  aria-haspopup="menu"
                  aria-expanded={menuId === n.id}
                  title="More — edit or delete"
                  data-testid={"note-more-" + n.id}
                  onclick={() => { confirmId = null; menuId = menuId === n.id ? null : n.id; }}
                  class="flex h-7 w-7 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800"
                >
                  <svg viewBox="0 0 16 16" class="h-4 w-4" fill="currentColor" aria-hidden="true">
                    <circle cx="8" cy="3.5" r="1.25"></circle>
                    <circle cx="8" cy="8" r="1.25"></circle>
                    <circle cx="8" cy="12.5" r="1.25"></circle>
                  </svg>
                </button>

                {#if menuId === n.id}
                  <!-- Click-away, so the menu never strands the row in a
                       half-open state the next click has to undo. -->
                  <button
                    type="button"
                    tabindex="-1"
                    aria-label="Close menu"
                    onclick={closeMenus}
                    class="fixed inset-0 z-20 cursor-default"
                  ></button>
                  <div
                    role="menu"
                    class="absolute right-0 top-8 z-30 w-44 overflow-hidden rounded-lg border border-white-300 bg-white-100 py-1 shadow-lg dark:border-navy-600 dark:bg-navy-800"
                  >
                    <button
                      type="button"
                      role="menuitem"
                      onclick={() => startEdit(n)}
                      class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-black-800 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
                    >
                      <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                        <path d="M11 2.5l2.5 2.5L6 12.5H3.5V10L11 2.5z" stroke-linejoin="round"></path>
                      </svg>
                      Edit note
                    </button>

                    {#if confirmId === n.id}
                      <!-- The second step is the whole point: a note is a
                           written record, and one stray click should not end
                           it. It names what goes, and says so in words. -->
                      <div class="border-t border-white-300 px-3 py-2 dark:border-navy-600">
                        <p class="text-[11px] leading-relaxed text-black-800 dark:text-black-600">
                          Delete this note? It cannot be undone.
                        </p>
                        <div class="mt-1.5 flex items-center gap-2">
                          <button
                            type="button"
                            data-testid={"note-delete-confirm-" + n.id}
                            onclick={() => remove(n)}
                            class="rounded-lg bg-neg-400 px-2 py-1 text-[11px] font-semibold text-white-100 transition-colors hover:opacity-90"
                          >Delete</button>
                          <button
                            type="button"
                            onclick={() => { confirmId = null; }}
                            class="rounded-lg px-2 py-1 text-[11px] text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
                          >Keep</button>
                        </div>
                      </div>
                    {:else}
                      <button
                        type="button"
                        role="menuitem"
                        data-testid={"note-delete-" + n.id}
                        onclick={() => { confirmId = n.id; }}
                        class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-neg-400 transition-colors hover:bg-neg-100"
                      >
                        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                          <path d="M3 5h10M6.5 5V3.5h3V5M5 5l.5 8h5L11 5" stroke-linecap="round" stroke-linejoin="round"></path>
                        </svg>
                        Delete note
                      </button>
                    {/if}
                  </div>
                {/if}
              </div>
            </div>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>
