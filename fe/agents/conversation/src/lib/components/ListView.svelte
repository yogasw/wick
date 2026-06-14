<script lang="ts">
  import { onMount } from "svelte";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError } from "@wick-fe/common-stores";
  import SessionList from "./SessionList.svelte";
  import { listSessions } from "../api/sessions.js";
  import { push } from "../router.js";
  import type { SessionListItem } from "../types/agents.js";

  type Props = {
    base: string;
  };

  let { base }: Props = $props();

  let sessions = $state<SessionListItem[]>([]);
  let loading = $state(true);
  let error = $state("");
  let search = $state("");

  onMount(() => {
    Effect.runPromise(
      listSessions(base).pipe(Effect.provide(WickClientLayer))
    )
      .then((res) => {
        sessions = res.sessions;
        loading = false;
      })
      .catch((err: unknown) => {
        const msg = err instanceof Error ? err.message : String(err);
        error = msg;
        loading = false;
        toastError(`Failed to load sessions: ${msg}`);
      });
  });
</script>

<div class="flex flex-col h-full p-6 max-w-2xl mx-auto w-full">
  <h1 class="text-xl font-semibold text-black-900 dark:text-white-100 mb-4">Sessions</h1>

  {#if loading}
    <div class="flex items-center justify-center py-16">
      <svg class="h-6 w-6 animate-spin text-green-500" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
      </svg>
      <span class="ml-2 text-sm text-black-600 dark:text-black-700">Loading sessions…</span>
    </div>
  {:else if error}
    <div class="rounded-xl border border-neg-300 dark:border-neg-700 bg-neg-50 dark:bg-neg-900/20 px-4 py-3 text-sm text-neg-700 dark:text-neg-400">
      {error}
    </div>
  {:else}
    <SessionList
      {sessions}
      {search}
      onSearch={(s) => { search = s; }}
      onSelect={(id) => push(`/sessions/${id}`)}
    />
  {/if}
</div>
