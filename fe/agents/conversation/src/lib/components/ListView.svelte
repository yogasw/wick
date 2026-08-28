<script lang="ts">
  import { onMount } from "svelte";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError } from "@wick-fe/common-stores";
  import SessionList from "./SessionList.svelte";
  import ProjectLanding from "./ProjectLanding.svelte";
  import OwnerTabs from "./OwnerTabs.svelte";
  import { listSessions } from "../api/sessions.js";
  import { getProjectOptions, getProviderOptions, pinProject } from "../api/options.js";
  import { push } from "../router.js";
  import type { SessionListItem, ProjectOption, ProviderOption } from "../types/agents.js";

  type Props = {
    base: string;
  };

  let { base }: Props = $props();

  const projectId = new URLSearchParams(window.location.search).get("project") ?? "";

  let sessions = $state<SessionListItem[]>([]);
  /* How many sessions matched the current scope BEFORE the server's page
     window — the honest number the list prints at its end. */
  let sessionTotal = $state(0);
  let hasMore = $state(false);
  let loading = $state(true);
  let listLoading = $state(false);
  let error = $state("");
  let search = $state("");

  /* Scope of the list: the caller's own sessions by default. "all" is a
     click away and fetches (and counts) only then — a page over a project
     with hundreds of chats starts by loading just yours, one page at a
     time. */
  let ownerTab = $state<"me" | "all">("me");

  function loadSessions(append = false) {
    listLoading = true;
    const offset = append ? sessions.length : 0;
    Effect.runPromise(
      listSessions(base, projectId || undefined, ownerTab, offset).pipe(
        Effect.provide(WickClientLayer),
      ),
    )
      .then((res) => {
        sessions = append ? [...sessions, ...res.sessions] : res.sessions;
        sessionTotal = res.total;
        hasMore = res.hasMore;
      })
      .catch((err: unknown) => {
        const msg = err instanceof Error ? err.message : String(err);
        error = msg;
        toastError(`Failed to load sessions: ${msg}`);
      })
      .finally(() => {
        listLoading = false;
        loading = false;
      });
  }

  function setOwnerTab(v: "me" | "all") {
    if (ownerTab === v) return;
    ownerTab = v;
    loadSessions();
  }

  let project = $state<ProjectOption | null>(null);
  /* Distinguishes "still fetching the project" from "fetched, and it does
     not exist" — without it an unknown project id would spin forever. */
  let projectLoaded = $state(false);
  let providers = $state<ProviderOption[]>([]);

  onMount(() => {
    loadSessions();
    if (projectId) {
      const projectEffect = getProjectOptions(base).pipe(Effect.provide(WickClientLayer));
      const providerEffect = getProviderOptions(base).pipe(Effect.provide(WickClientLayer));
      Effect.runPromise(Effect.all([projectEffect, providerEffect]))
        .then(([projects, provs]) => {
          project = projects.find((p) => p.id === projectId) ?? null;
          providers = provs;
        })
        .catch((err: unknown) => {
          const msg = err instanceof Error ? err.message : String(err);
          error = msg;
          toastError(`Failed to load project: ${msg}`);
        })
        .finally(() => {
          projectLoaded = true;
        });
    }
  });

  function handlePin() {
    if (!projectId || !project) return;
    const prev = project.pinned;
    /* optimistic flip so the button reacts instantly; reconcile with the
       server's authoritative `pinned` (the endpoint toggles) and roll back on
       error. */
    project = { ...project, pinned: !prev };
    Effect.runPromise(pinProject(base, projectId).pipe(Effect.provide(WickClientLayer)))
      .then((res) => {
        if (project) project = { ...project, pinned: res.pinned };
      })
      .catch((err: unknown) => {
        if (project) project = { ...project, pinned: prev };
        toastError(`Failed to pin project: ${err instanceof Error ? err.message : String(err)}`);
      });
  }
</script>

{#if loading || (projectId && !projectLoaded && !error)}
  <div class="flex items-center justify-center py-16 h-full">
    <svg class="h-6 w-6 animate-spin text-green-500" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
      <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
    </svg>
    <span class="ml-2 text-sm text-black-600 dark:text-black-700">Loading…</span>
  </div>
{:else if error}
  <div class="p-6">
    <div class="rounded-xl border border-neg-300 dark:border-neg-700 bg-neg-50 dark:bg-neg-900/20 px-4 py-3 text-sm text-neg-700 dark:text-neg-400">
      {error}
    </div>
  </div>
{:else if projectId && project}
  <ProjectLanding
    {base}
    {project}
    {providers}
    {sessions}
    {sessionTotal}
    {ownerTab}
    {listLoading}
    {hasMore}
    onLoadMore={() => loadSessions(true)}
    onOwnerTab={setOwnerTab}
    onPin={handlePin}
    onSelectSession={(id) => push(`/sessions/${id}`)}
  />
{:else if projectId && !project}
  <div class="flex flex-col h-full p-6 max-w-2xl mx-auto w-full">
    <div class="rounded-xl border border-cau-300 dark:border-cau-700 bg-cau-50 dark:bg-cau-900/20 px-4 py-3 text-sm text-cau-700 dark:text-cau-400">
      Project not found.
    </div>
  </div>
{:else}
  <div class="flex flex-col h-full p-6 max-w-2xl mx-auto w-full">
    <div class="mb-4 flex items-center justify-between gap-3">
      <h1 class="text-xl font-semibold text-black-900 dark:text-white-100">Sessions</h1>
      <OwnerTabs value={ownerTab} onChange={setOwnerTab} />
    </div>
    <SessionList
      {sessions}
      total={sessionTotal}
      loading={listLoading}
      {hasMore}
      onLoadMore={() => loadSessions(true)}
      {search}
      newChatHref={`${base}/`}
      onSearch={(s) => { search = s; }}
      onSelect={(id) => push(`/sessions/${id}`)}
    />
  </div>
{/if}
