<script lang="ts">
  import type { SessionListItem } from "../types/agents.js";

  type Props = {
    sessions: SessionListItem[];
    selectedId?: string;
    search: string;
    pageSize?: number;
    newChatHref?: string;
    onSearch: (s: string) => void;
    onSelect: (id: string) => void;
    onDelete?: (id: string) => void;
  };

  let {
    sessions,
    selectedId,
    search,
    pageSize = 10,
    newChatHref,
    onSearch,
    onSelect,
    onDelete,
  }: Props = $props();

  let currentPage = $state(1);
  let openKebabId = $state<string | null>(null);

  function toggleKebab(e: MouseEvent, id: string) {
    e.stopPropagation();
    openKebabId = openKebabId === id ? null : id;
  }

  function handleDelete(e: MouseEvent, id: string) {
    e.stopPropagation();
    openKebabId = null;
    onDelete!(id);
  }

  const filtered = $derived(
    search.trim() === ""
      ? sessions
      : sessions.filter((s) =>
          s.label.toLowerCase().includes(search.trim().toLowerCase())
        )
  );

  const totalPages = $derived(Math.max(1, Math.ceil(filtered.length / pageSize)));

  $effect(() => {
    search;
    currentPage = 1;
  });

  const paginated = $derived(
    filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize)
  );

  const showPager = $derived(filtered.length > pageSize);

  function formatLastActive(ts: string): string {
    if (!ts) return "";
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    const now = Date.now();
    const diff = Math.floor((now - d.getTime()) / 1000);
    if (diff < 60) return "just now";
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return `${Math.floor(diff / 86400)}d ago`;
  }

  function lifecycleCls(lc: string): string {
    const map: Record<string, string> = {
      working:  "bg-pos-100 text-pos-400",
      idle:     "bg-prog-100 text-prog-400",
      spawning: "bg-cau-100 text-cau-400",
      queued:   "bg-cau-100 text-cau-400",
      killed:   "bg-neg-100 text-neg-400",
      dead:     "bg-neg-100 text-neg-400",
      error:    "bg-neg-100 text-neg-400",
    };
    return map[lc] ?? "bg-white-300 dark:bg-navy-600 text-black-700";
  }
</script>

<div class="space-y-4">
  {#if newChatHref}
    <a
      href={newChatHref}
      class="inline-flex items-center gap-2 rounded-xl bg-green-600 hover:bg-green-700 active:bg-green-800 px-4 py-2.5 text-sm font-semibold text-white-100 transition-colors w-full justify-center"
    >
      <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M8 3v10M3 8h10" stroke-linecap="round"></path>
      </svg>
      New chat
    </a>
  {/if}
  <div class="relative">
    <svg
      viewBox="0 0 16 16"
      class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-black-600 dark:text-black-700 pointer-events-none"
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
    >
      <circle cx="6.5" cy="6.5" r="4.5"></circle>
      <path d="M10.5 10.5l3 3" stroke-linecap="round"></path>
    </svg>
    <input
      type="text"
      placeholder="Search chats..."
      value={search}
      oninput={(e) => onSearch((e.target as HTMLInputElement).value)}
      class="w-full rounded-xl border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 pl-9 pr-4 py-2.5 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
    />
  </div>

  {#if sessions.length === 0}
    <div class="flex flex-col items-center py-16 text-center">
      <p class="text-sm text-black-700 dark:text-black-600">No sessions yet.</p>
    </div>
  {:else if filtered.length === 0}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden">
      <p class="px-5 py-8 text-center text-sm text-black-600 dark:text-black-700">No chats match your search.</p>
    </div>
  {:else}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden divide-y divide-white-300 dark:divide-navy-600">
      {#each paginated as sess (sess.id)}
        {@const isSelected = sess.id === selectedId}
        <div
          data-testid={"session-row-" + sess.id}
          aria-current={isSelected ? "true" : undefined}
          role="button"
          tabindex="0"
          onclick={() => onSelect(sess.id)}
          onkeydown={(e) => e.key === "Enter" && onSelect(sess.id)}
          class={[
            "flex items-center justify-between gap-4 px-5 py-3.5 cursor-pointer transition-colors",
            isSelected
              ? "bg-green-50 dark:bg-green-900/10"
              : "hover:bg-white-200 dark:hover:bg-navy-800",
          ].join(" ")}
        >
          <div class="flex-1 min-w-0">
            <p class="truncate text-sm font-medium text-black-900 dark:text-white-100">
              {sess.label || "New session"}
            </p>
            <div class="flex items-center gap-2 mt-0.5">
              <span class="text-xs text-black-600 dark:text-black-700">
                {formatLastActive(sess.last_active)}
              </span>
              {#if sess.lifecycle}
                <span
                  class={"rounded px-1.5 py-0.5 text-[10px] font-medium " + lifecycleCls(sess.lifecycle)}
                >
                  {sess.lifecycle}
                </span>
              {/if}
              {#if sess.active_agent}
                <span class="text-xs text-black-600 dark:text-black-700 truncate">
                  {sess.active_agent}
                </span>
              {/if}
            </div>
          </div>
          {#if onDelete}
            <div class="relative shrink-0">
              <button
                type="button"
                aria-label="Row actions"
                onclick={(e) => toggleKebab(e, sess.id)}
                class="inline-flex items-center justify-center h-7 w-7 rounded-lg text-black-600 dark:text-black-700 hover:bg-white-300 dark:hover:bg-navy-600 transition-colors"
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4" fill="currentColor" aria-hidden="true">
                  <circle cx="8" cy="3" r="1.2"></circle>
                  <circle cx="8" cy="8" r="1.2"></circle>
                  <circle cx="8" cy="13" r="1.2"></circle>
                </svg>
              </button>
              {#if openKebabId === sess.id}
                <div class="absolute right-0 bottom-full mb-1 z-20 min-w-[120px] rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg py-1">
                  <button
                    type="button"
                    aria-label="Delete"
                    onclick={(e) => handleDelete(e, sess.id)}
                    class="w-full text-left px-3 py-2 text-xs text-neg-600 dark:text-neg-400 hover:bg-neg-50 dark:hover:bg-neg-900/20 transition-colors"
                  >Delete</button>
                </div>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    </div>

    {#if showPager}
      <div class="flex items-center justify-between pt-2">
        <button
          type="button"
          disabled={currentPage <= 1}
          onclick={() => { if (currentPage > 1) currentPage--; }}
          class="inline-flex items-center gap-1 text-sm text-green-600 dark:text-green-400 hover:underline disabled:opacity-40 disabled:cursor-default disabled:no-underline"
        >
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
          Prev
        </button>
        <span class="text-xs text-black-700 dark:text-black-600">
          Page {currentPage} / {totalPages}
        </span>
        <button
          type="button"
          disabled={currentPage >= totalPages}
          onclick={() => { if (currentPage < totalPages) currentPage++; }}
          class="inline-flex items-center gap-1 text-sm text-green-600 dark:text-green-400 hover:underline disabled:opacity-40 disabled:cursor-default disabled:no-underline"
        >
          Next
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
        </button>
      </div>
    {/if}
  {/if}
</div>
