<script lang="ts">
  /* Wiki tab — find what agents wrote down, then READ it (PLAN §13.3).

     Search only ever returns a snippet, so a tab that stopped there would
     show fragments of a memory nobody can open. The page body arrives from a
     separate read addressed by exact path.

     The raw per-session observations live in the page body, not in their own
     request, and that is a limit rather than a shortcut: the backend has no
     CLI subcommand for them, and its MCP tool would be a third exception to
     this dashboard's no-MCP rule — authorised for two calls, and this is not
     one of them (PLAN §13.2.1, §20.2). So the tab reads the section the
     consolidator already wrote, and says so when a page has none. */
  import { Button, Select, TextInput } from "@wick-fe/common-ui";
  import { Section } from "@wick-fe/common-agentmemory";
  import { BlockedState } from "@wick-fe/common-agentmemory";
  import { blockedBy } from "./format.js";
  import {
    frontmatterRows,
    hitKey,
    hitScope,
    hitTitle,
    pageScope,
    rawObservations,
    SEARCH_EMPTY,
    snippetHTML,
  } from "./wiki.js";
  import type { Page, ProjectRow, SearchHit, SearchResponse } from "./types.js";

  type Props = {
    res: SearchResponse | null;
    page: Page | null;
    pageError: string;
    projects: ProjectRow[];
    scopeKey: string;
    query: string;
    searching: boolean;
    loadingPage: boolean;
    selected: string;
    onQuery: (q: string) => void;
    onScope: (key: string) => void;
    onSearch: () => void;
    onOpen: (h: SearchHit) => void;
    onGoOverview: () => void;
  };
  let {
    res,
    page,
    pageError,
    projects,
    scopeKey,
    query,
    searching,
    loadingPage,
    selected,
    onQuery,
    onScope,
    onSearch,
    onOpen,
    onGoOverview,
  }: Props = $props();

  const blocked = $derived(blockedBy(res));
  const hits = $derived(res?.hits ?? []);
  const raw = $derived(rawObservations(page));
  const frontmatter = $derived(frontmatterRows(page));

  // The scope picker defaults to the whole store. Every hit still carries its
  // own project label, because a store-wide result set mixes them and an
  // unlabelled list of session titles from four projects is unreadable.
  const scopeOptions = $derived([
    { label: "Every project", value: "" },
    ...projects.map((p) => ({ label: `${p.workspace}/${p.project}`, value: `${p.workspace}/${p.project}` })),
  ]);

  function onKey(e: KeyboardEvent): void {
    if (e.key === "Enter") onSearch();
  }
</script>

<div class="mx-auto w-full max-w-5xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
  <!-- Search bar -->
  <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
    <div class="min-w-0 flex-1" onkeydown={onKey} role="search">
      <TextInput
        value={query}
        onChange={onQuery}
        type="search"
        placeholder="Search pages — whole words only, so type an identifier in full"
        ariaLabel="Search the wiki"
      />
    </div>
    <div class="w-full sm:w-56">
      <Select value={scopeKey} options={scopeOptions} onChange={onScope} size="md" />
    </div>
    <Button variant="primary" size="md" disabled={searching} onclick={onSearch}>
      {searching ? "Searching…" : "Search"}
    </Button>
  </div>

  {#if blocked}
    <BlockedState {blocked} onAction={onGoOverview} />
  {:else}
    <div class="grid gap-5 lg:grid-cols-[22rem_1fr]">
      <!-- Results -->
      <Section
        title="Results"
        scope={scopeKey || "every project"}
        note={res?.note}
      >
        {#if searching}
          <p class="px-5 py-8 text-center text-xs text-black-700 dark:text-black-600">Searching…</p>
        {:else if !res}
          <p class="px-5 py-8 text-center text-xs leading-relaxed text-black-700 dark:text-black-600">
            Search for a page, or open one from a project's latest-pages list.
          </p>
        {:else if hits.length === 0}
          <p class="px-5 py-6 text-xs leading-relaxed text-black-700 dark:text-black-600">{SEARCH_EMPTY}</p>
        {:else}
          <ul class="divide-y divide-white-300 dark:divide-navy-600">
            {#each hits as h (hitKey(h))}
              {@const key = hitKey(h)}
              <li>
                <button
                  type="button"
                  onclick={() => onOpen(h)}
                  class={`w-full px-5 py-3 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-green-200 dark:focus-visible:ring-green-800 ${
                    key === selected
                      ? "bg-green-200 dark:bg-green-800"
                      : "hover:bg-white-200 dark:hover:bg-navy-800"
                  }`}
                >
                  <p class="truncate text-xs font-medium text-black-900 dark:text-white-100">{hitTitle(h)}</p>
                  <p class="mt-0.5 flex flex-wrap items-center gap-2 text-[0.6875rem] text-black-700 dark:text-black-600">
                    <span class="font-mono">{hitScope(h)}</span>
                    {#if h.kind}<span class="rounded-full bg-white-200 dark:bg-navy-800 px-2 py-0.5">{h.kind}</span>{/if}
                  </p>
                  {#if h.snippet}
                    <!-- The backend wraps matches in <mark>; snippetHTML
                         escapes everything else, because a page body is
                         agent-written text that can contain anything. -->
                    <p class="mt-1 line-clamp-3 text-[0.6875rem] leading-relaxed text-black-800 dark:text-black-600">
                      {@html snippetHTML(h.snippet)}
                    </p>
                  {/if}
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </Section>

      <!-- The page -->
      <div class="space-y-5">
        {#if loadingPage}
          <Section title="Page"><p class="px-5 py-10 text-center text-xs text-black-700 dark:text-black-600">Reading…</p></Section>
        {:else if pageError}
          <Section title="Page">
            <p class="px-5 py-6 text-xs leading-relaxed text-neg-400 dark:text-neg-400">{pageError}</p>
          </Section>
        {:else if !page}
          <Section title="Page">
            <p class="px-5 py-10 text-center text-xs leading-relaxed text-black-700 dark:text-black-600">
              Pick a result to read it in full. Search shows an excerpt; this is the whole page as the agent left it.
            </p>
          </Section>
        {:else}
          <Section title={page.title || page.path} scope={pageScope(page)}>
            <p class="border-b border-white-300 dark:border-navy-600 px-5 py-2 font-mono text-[0.6875rem] text-black-700 dark:text-black-600">
              {page.path}
            </p>
            <pre
              class="max-h-[32rem] overflow-auto whitespace-pre-wrap break-words px-5 py-4 font-mono text-xs leading-relaxed text-black-900 dark:text-white-100">{page.body}</pre>
          </Section>

          <Section
            title="Raw observations"
            scope="this session"
            note="The session's own event log, as the backend recorded it."
          >
            {#if raw.present}
              <ol class="divide-y divide-white-300 dark:divide-navy-600">
                {#each raw.lines as line, i (i)}
                  <li class="px-5 py-2 font-mono text-[0.6875rem] leading-relaxed text-black-900 dark:text-white-100">
                    {line}
                  </li>
                {/each}
              </ol>
            {:else}
              <p class="px-5 py-5 text-xs leading-relaxed text-black-700 dark:text-black-600">{raw.reason}</p>
            {/if}
          </Section>

          {#if frontmatter.length}
            <Section title="Page metadata" scope="this page">
              <dl class="divide-y divide-white-300 dark:divide-navy-600">
                {#each frontmatter as row (row.key)}
                  <div class="flex flex-wrap gap-2 px-5 py-2">
                    <dt class="w-40 shrink-0 text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">
                      {row.key}
                    </dt>
                    <dd class="min-w-0 flex-1 break-words font-mono text-[0.6875rem] text-black-900 dark:text-white-100">
                      {row.value}
                    </dd>
                  </div>
                {/each}
              </dl>
            </Section>
          {/if}
        {/if}
      </div>
    </div>
  {/if}
</div>
