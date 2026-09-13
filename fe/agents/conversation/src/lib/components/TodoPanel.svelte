<!--
  Purpose:    The session's todo list, as a panel rather than a card buried
              in the scrollback. The same list the agent is working from —
              so the person watching and the agent doing are looking at one
              thing — plus the lists that came before it.
  Caller:     DetailView.svelte (rail tab "todos")
  Dependencies: api/todos.ts types
-->
<script lang="ts">
  import type { TodoList, TodoItem } from "../api/todos.js";

  type Props = {
    active: TodoList | null;
    history: TodoList[];
    loading: boolean;
    error: string | null;
    onRefresh: () => void;
  };
  let { active, history, loading, error, onRefresh }: Props = $props();

  /* Only one past list is expanded at a time: the history is a reference,
     not a second thing to read top to bottom. */
  let openHistory = $state<number | null>(null);

  function mark(status: string): string {
    if (status === "completed") return "✓";
    if (status === "in_progress") return "◐";
    return "";
  }
  function itemCls(status: string): string {
    if (status === "completed") return "text-black-700 dark:text-black-600 line-through";
    if (status === "in_progress") return "text-black-900 dark:text-white-100 font-medium";
    return "text-black-800 dark:text-black-600";
  }
  function dotCls(status: string): string {
    const base = "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[0.6rem] font-semibold ";
    if (status === "completed") return base + "bg-green-500 text-white-100";
    if (status === "in_progress") return base + "bg-green-500 text-white-100 animate-pulse";
    return base + "border border-white-400 dark:border-navy-600 text-black-700 dark:text-black-600";
  }
  function when(iso?: string): string {
    if (!iso) return "";
    const t = new Date(iso).getTime();
    if (!Number.isFinite(t)) return "";
    const secs = Math.max(0, Math.round((Date.now() - t) / 1000));
    if (secs < 60) return `${secs}s ago`;
    if (secs < 3600) return `${Math.round(secs / 60)}m ago`;
    if (secs < 86400) return `${Math.round(secs / 3600)}h ago`;
    return `${Math.round(secs / 86400)}d ago`;
  }
  function summary(l: TodoList): string {
    return `${l.completed}/${l.total} done`;
  }
</script>

<div class="flex h-full flex-col overflow-hidden">
  <div class="flex items-center justify-between gap-2 border-b border-white-300 dark:border-navy-600 px-3 py-2">
    <span class="text-sm font-medium text-black-900 dark:text-white-100">Todo</span>
    <button
      type="button"
      onclick={onRefresh}
      class="rounded px-2 py-1 text-xs text-black-700 dark:text-black-600 hover:text-green-600"
      title="Reload from the server"
    >Refresh</button>
  </div>

  <div class="min-h-0 flex-1 overflow-y-auto px-3 py-3">
    {#if loading && !active && history.length === 0}
      <!-- Only on the FIRST load: a spinner over a list that is already on
           screen hides the thing the panel exists to show. -->
      <p class="text-xs text-black-700 dark:text-black-600">Loading the todo list…</p>
    {:else if error}
      <p class="text-xs text-neg-400">{error}</p>
    {:else if !active && history.length === 0}
      <p class="text-xs text-black-700 dark:text-black-600">
        No todo list yet. One appears here as soon as the agent writes one — it is the same list it works from.
      </p>
    {:else}
      {#if active}
        <div class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 p-3">
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
              {active.done ? "Finished" : "In progress"}
            </span>
            <span class="font-mono text-xs text-black-700 dark:text-black-600">{summary(active)}</span>
          </div>
          <ul class="mt-2 space-y-1.5">
            {#each active.items as it (it.id || it.label)}
              <li class="flex items-start gap-2">
                <span class={dotCls(it.status)}>{mark(it.status)}</span>
                <div class="min-w-0">
                  <p class={"break-words text-sm " + itemCls(it.status)}>{it.label}</p>
                  {#if it.description}
                    <p class="break-words text-xs text-black-700 dark:text-black-600">{it.description}</p>
                  {/if}
                  {#if it.substeps && it.substeps.length}
                    <ul class="mt-1 space-y-1">
                      {#each it.substeps as sub (sub.step)}
                        <li class="flex items-start gap-2">
                          <span class={dotCls(sub.status)}>{mark(sub.status)}</span>
                          <span class={"break-words text-xs " + itemCls(sub.status)}>{sub.step}</span>
                        </li>
                      {/each}
                    </ul>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
          {#if active.updated_at}
            <p class="mt-2 text-[0.7rem] text-black-700 dark:text-black-600">updated {when(active.updated_at)}</p>
          {/if}
        </div>
      {/if}

      {#if history.length}
        <p class="mt-4 text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
          Earlier ({history.length})
        </p>
        <ul class="mt-1 space-y-1">
          {#each history as h, i (h.started_at || i)}
            <li class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
              <button
                type="button"
                class="flex w-full items-center justify-between gap-2 px-3 py-2 text-left"
                onclick={() => { openHistory = openHistory === i ? null : i; }}
              >
                <span class="min-w-0 truncate text-xs text-black-800 dark:text-black-600">
                  {h.items[0]?.label ?? "checklist"}
                </span>
                <span class="shrink-0 font-mono text-[0.7rem] text-black-700 dark:text-black-600">
                  {summary(h)}{h.updated_at ? ` · ${when(h.updated_at)}` : ""}
                </span>
              </button>
              {#if openHistory === i}
                <ul class="space-y-1 px-3 pb-2">
                  {#each h.items as it (it.id || it.label)}
                    <li class="flex items-start gap-2">
                      <span class={dotCls(it.status)}>{mark(it.status)}</span>
                      <span class={"break-words text-xs " + itemCls(it.status)}>{it.label}</span>
                    </li>
                  {/each}
                </ul>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </div>
</div>
