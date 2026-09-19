<!--
  Purpose:    The session's todo list, as a panel rather than a card buried
              in the scrollback. The same list the agent is working from —
              so the person watching and the agent doing are looking at one
              thing — plus the lists that came before it.
  Caller:     DetailView.svelte (rail tab "todos")
  Dependencies: api/todos.ts types
-->
<script lang="ts">
  import type { TodoList, TodoItem, TodoDetail } from "../api/todos.js";
  import { renderMarkdown } from "../markdown.js";
  import HtmlArtifact from "./HtmlArtifact.svelte";

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

  /* Which items have their payload open. A detail is a log tail or a JSON
     blob — worth having, not worth reading every time the panel opens. */
  let openDetail = $state<Record<string, boolean>>({});
  const keyOf = (it: TodoItem) => it.id || it.label;

  function mark(status: string): string {
    if (status === "completed") return "✓";
    if (status === "in_progress") return "◐";
    if (status === "failed") return "!";
    if (status === "stopped") return "×";
    return "";
  }
  function itemCls(status: string): string {
    if (status === "completed") return "text-black-700 dark:text-black-600 line-through";
    if (status === "in_progress") return "text-black-900 dark:text-white-100 font-medium";
    if (status === "failed") return "text-neg-400 font-medium";
    if (status === "stopped") return "text-black-700 dark:text-black-600";
    return "text-black-800 dark:text-black-600";
  }
  function dotCls(status: string): string {
    const base = "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[0.6rem] font-semibold ";
    if (status === "completed") return base + "bg-green-500 text-white-100";
    if (status === "in_progress") return base + "bg-green-500 text-white-100 animate-pulse";
    if (status === "failed") return base + "bg-neg-400 text-white-100";
    if (status === "stopped") return base + "bg-white-400 dark:bg-navy-600 text-black-800 dark:text-black-600";
    return base + "border border-white-400 dark:border-navy-600 text-black-700 dark:text-black-600";
  }
  /* A bar only means something against a total. Without one the counter is
     still worth showing ("1483 lines"), so the caller is not forced to know
     the end before it can report the middle. */
  function pct(p: { done: number; total: number }): number {
    if (!p.total || p.total <= 0) return 0;
    return Math.max(0, Math.min(100, Math.round((p.done / p.total) * 100)));
  }
  /* A detail that keeps growing is a log, and a log is read from the bottom.
     Stick to the tail on every update — but only while the reader is already
     there: yanking the viewport down while somebody is reading further up is
     worse than making them scroll. */
  function tail(node: HTMLElement) {
    const atBottom = () => node.scrollHeight - node.scrollTop - node.clientHeight < 24;
    let stick = true;
    const onScroll = () => (stick = atBottom());
    node.addEventListener("scroll", onScroll, { passive: true });
    node.scrollTop = node.scrollHeight;
    const obs = new MutationObserver(() => {
      if (stick) node.scrollTop = node.scrollHeight;
    });
    obs.observe(node, { childList: true, characterData: true, subtree: true });
    return {
      destroy() {
        obs.disconnect();
        node.removeEventListener("scroll", onScroll);
      },
    };
  }

  /* The last non-empty line, for the collapsed state. A log's verdict is its
     final line — "BUILD DONE", "GATE PASS", "FAIL: TestThing" — so showing it
     next to the toggle answers the question without opening anything. */
  function lastLine(d: TodoDetail): string {
    const lines = d.body.split("\n").filter((l) => l.trim() !== "");
    const last = lines[lines.length - 1] ?? "";
    return last.length > 80 ? last.slice(0, 80) + "…" : last;
  }

  /* JSON is pretty-printed when it parses and shown as-is when it does not:
     a payload that arrives malformed is exactly when you want to see it
     verbatim rather than have the panel swallow it. */
  function bodyText(d: TodoDetail): string {
    if (d.format !== "json") return d.body;
    try {
      return JSON.stringify(JSON.parse(d.body), null, 2);
    } catch {
      return d.body;
    }
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
            <span class="text-xs font-medium uppercase tracking-wide {active.stopped ? 'text-neg-400' : 'text-black-700 dark:text-black-600'}">
              {active.stopped ? "Stopped" : active.done ? "Finished" : "In progress"}
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
                  {#if it.progress}
                    <div class="mt-1 flex items-center gap-2">
                      {#if it.progress.total > 0}
                        <div class="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
                          <div
                            class="h-full rounded-full {it.status === 'failed' ? 'bg-neg-400' : 'bg-green-500'} transition-[width] duration-300"
                            style={`width:${pct(it.progress)}%`}
                          ></div>
                        </div>
                      {/if}
                      <span class="shrink-0 font-mono text-[0.7rem] text-black-700 dark:text-black-600">
                        {it.progress.done}{it.progress.total > 0 ? `/${it.progress.total}` : ""}{it.progress.label ? ` ${it.progress.label}` : ""}
                      </span>
                    </div>
                  {/if}
                  {#if it.detail}
                    {@const open = openDetail[keyOf(it)]}
                    <button
                      type="button"
                      class="mt-1 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[0.7rem] font-medium text-black-700 transition-colors hover:bg-white-300 hover:text-black-900 dark:text-black-600 dark:hover:bg-navy-600 dark:hover:text-white-100"
                      aria-expanded={open}
                      onclick={() => (openDetail = { ...openDetail, [keyOf(it)]: !open })}
                    >
                      <svg
                        class="h-3 w-3 shrink-0 transition-transform {open ? 'rotate-90' : ''}"
                        viewBox="0 0 20 20"
                        fill="currentColor"
                        aria-hidden="true"
                      >
                        <path fill-rule="evenodd" d="M7.21 14.77a.75.75 0 0 1 .02-1.06L11.168 10 7.23 6.29a.75.75 0 1 1 1.04-1.08l4.5 4.25a.75.75 0 0 1 0 1.08l-4.5 4.25a.75.75 0 0 1-1.06-.02Z" clip-rule="evenodd"></path>
                      </svg>
                      <span>detail</span>
                      <span class="font-mono text-black-600 dark:text-black-700">{it.detail.format ?? "text"}</span>
                    </button>
                    {#if !open && it.detail.format !== "html"}
                      <p class="truncate font-mono text-[0.7rem] text-black-700 dark:text-black-600" title={lastLine(it.detail)}>
                        {lastLine(it.detail)}
                      </p>
                    {/if}
                    {#if open}
                      {#if it.detail.format === "html"}
                        <div class="mt-1 overflow-hidden rounded-lg border border-white-300 dark:border-navy-600">
                          <HtmlArtifact src={it.detail.body} name="detail.html" />
                        </div>
                      {:else if it.detail.format === "markdown"}
                        <!-- eslint-disable-next-line svelte/no-at-html-tags -->
                        <div class="markdown-body mt-1 rounded-lg border border-white-300 bg-white-200 p-2 text-xs text-black-800 dark:border-navy-600 dark:bg-navy-800 dark:text-black-600">
                          {@html renderMarkdown(it.detail.body)}
                        </div>
                      {:else}
                        <pre
                          use:tail
                          class="mt-1 max-h-56 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-white-300 bg-white-200 p-2 font-mono text-[0.7rem] leading-relaxed text-black-800 dark:border-navy-600 dark:bg-navy-800 dark:text-black-600"
                        >{bodyText(it.detail)}</pre>
                      {/if}
                    {/if}
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
          {#if active.note}
            <p class="mt-2 break-words text-[0.7rem] text-neg-400">{active.note}</p>
          {/if}
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
                <span class="shrink-0 font-mono text-[0.7rem] {h.stopped ? 'text-neg-400' : 'text-black-700 dark:text-black-600'}">
                  {summary(h)}{h.stopped ? " · stopped" : ""}{h.updated_at ? ` · ${when(h.updated_at)}` : ""}
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
