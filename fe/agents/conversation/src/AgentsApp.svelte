<script lang="ts">
  /* The Team app (/team; "Agents" in code): a messaging-style roster on the left and the
     chosen agent's chat on the right. The chat IS the ordinary
     conversation view (DetailView) in agent mode, so streaming, approvals,
     artifacts and the rail all come along instead of being rebuilt.
     Settings, other conversations and the + Agent wizard are drawers with
     their own URL (agentsRouter.ts), so they survive a refresh. */
  import { onMount } from "svelte";
  import { KebabMenu, ToastHost } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import DetailView from "./lib/components/DetailView.svelte";
  import AgentAvatar from "./lib/components/AgentAvatar.svelte";
  import AgentSettings from "./lib/components/AgentSettings.svelte";
  import AgentWizard from "./lib/components/AgentWizard.svelte";
  import AgentSessions from "./lib/components/AgentSessions.svelte";
  import { agentsRoute, navigate, type AgentsRoute, type AgentsPanel } from "./lib/agentsRouter.js";
  import { hiddenTabsFor } from "./lib/agentMode.js";
  import { rosterTime } from "./lib/timeFormat.js";
  import { listAgents, openAgentChat, runApi, isWorking, type AgentItem } from "./lib/api/team.js";

  const appEl = document.getElementById("app");
  const base = appEl?.dataset.base ?? "";
  const viewerName = appEl?.dataset.viewerName ?? "";
  const viewerInitial = (viewerName.trim()[0] ?? "?").toUpperCase();

  let route = $state<AgentsRoute>({ handle: null, session: null, panel: null });
  agentsRoute.subscribe((v) => { route = v; });

  let agents = $state<AgentItem[]>([]);
  let captainId = $state("");
  let loaded = $state(false);
  let loadError = $state("");
  let query = $state("");
  // Below lg the roster is a drawer over the chat, opened from the header.
  let rosterOpen = $state(false);

  async function load() {
    try {
      const r = await runApi(listAgents(base));
      agents = r.agents ?? [];
      captainId = r.captain_id ?? "";
      loadError = "";
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      loaded = true;
    }
  }

  // Status dots go stale without a refresh; the roster is small and the
  // list endpoint reads only the in-memory registry, so a slow poll is fine.
  onMount(() => {
    load();
    const t = setInterval(() => {
      if (document.visibilityState === "visible") load();
    }, 15000);
    return () => clearInterval(t);
  });

  const captain = $derived(agents.find((a) => a.id === captainId) ?? agents.find((a) => a.is_captain));

  /* Captain pinned on top, the rest by last activity, newest first. */
  const roster = $derived.by(() => {
    const q = query.trim().toLowerCase();
    const list = agents.filter(
      (a) => !q || a.name.toLowerCase().includes(q) || a.handle.toLowerCase().includes(q),
    );
    const ts = (a: AgentItem) => (a.last_active ? Date.parse(a.last_active) || 0 : 0);
    return list.sort((a, b) => {
      if (a.is_captain !== b.is_captain) return a.is_captain ? -1 : 1;
      return ts(b) - ts(a) || a.name.localeCompare(b.name);
    });
  });

  const selected = $derived(
    route.handle ? agents.find((a) => a.handle === route.handle) : captain,
  );

  /* /team names no agent: settle on the Captain, rewriting the URL rather
     than adding a history entry. An unknown handle does the same, once the
     list has actually loaded. */
  $effect(() => {
    if (!loaded || loadError) return;
    if (route.handle && !agents.some((a) => a.handle === route.handle)) {
      toastError(`Agent @${route.handle} tidak ditemukan`);
      navigate({ handle: captain?.handle ?? null, session: null, panel: route.panel }, { replace: true });
      return;
    }
    if (!route.handle && captain && route.panel?.kind !== "new") {
      navigate({ ...route, handle: captain.handle }, { replace: true });
    }
  });

  /* The main chat is created lazily on first open. Guarded per agent so a
     re-render while the POST is in flight does not create a second one. */
  let opening = $state<string | null>(null);
  $effect(() => {
    const a = selected;
    if (!a || route.session || a.main_session_id || opening === a.id) return;
    opening = a.id;
    runApi(openAgentChat(base, a.id))
      .then((r) => {
        agents = agents.map((x) => (x.id === a.id ? { ...x, main_session_id: r.session_id } : x));
      })
      .catch((e) => toastError(`Buka chat: ${e instanceof Error ? e.message : String(e)}`))
      .finally(() => { opening = null; });
  });

  const chatSessionId = $derived(route.session ?? selected?.main_session_id ?? "");

  function go(patch: Partial<AgentsRoute>, replace = false) {
    navigate({ ...route, ...patch }, { replace });
  }
  function openAgent(a: AgentItem) {
    rosterOpen = false;
    navigate({ handle: a.handle, session: null, panel: null });
  }
  function openPanel(panel: AgentsPanel | null) {
    go({ panel });
  }

  async function newChat() {
    if (!selected) return;
    try {
      const r = await runApi(openAgentChat(base, selected.id, true));
      go({ session: r.session_id, panel: null });
    } catch (e) {
      toastError(`Chat baru: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape" && route.panel) {
      e.preventDefault();
      openPanel(null);
    }
  }

  function onSaved(next: AgentItem) {
    const prevHandle = selected?.handle;
    agents = agents.map((a) => (a.id === next.id ? { ...a, ...next } : next.is_captain ? { ...a, is_captain: false } : a));
    if (next.is_captain) captainId = next.id;
    // A renamed handle moves the page with it, or the next refresh 404s.
    if (prevHandle && next.handle !== prevHandle) go({ handle: next.handle }, true);
  }

  function onDeleted(id: string) {
    agents = agents.filter((a) => a.id !== id);
    navigate({ handle: captain?.handle ?? null, session: null, panel: null }, { replace: true });
  }

  /* A new agent hatches: its avatar is an egg for a moment, then pops. */
  const HATCH_MS = 2600;
  let hatching = $state<string[]>([]);

  function onCreated(a: AgentItem) {
    agents = [...agents, a];
    hatching = [...hatching, a.id];
    setTimeout(() => (hatching = hatching.filter((id) => id !== a.id)), HATCH_MS);
    navigate({ handle: a.handle, session: null, panel: null });
  }

  const menuItems = $derived([
    { label: "Settings", onclick: () => openPanel({ kind: "settings", tab: "persona" }) },
    { label: "Percakapan lain", onclick: () => openPanel({ kind: "sessions" }) },
    { label: "Chat baru", onclick: newChat },
  ]);

  const agentMode = $derived({
    hideTabs: hiddenTabsFor(selected?.features),
    hideHeader: true,
    hidePickers: true,
    onDeleted: () => go({ session: null }),
  });

  function rowPreview(a: AgentItem): string {
    if (a.disabled) return "Nonaktif";
    return a.last_preview || a.description || "Belum ada percakapan";
  }

  /* Hover tip on a roster row. Only what the list already knows; the
     current action ("lagi query Loki…") needs a server field first. */
  function rowTip(a: AgentItem): string {
    if (isWorking(a.status)) return "sedang mengetik";
    if (a.disabled) return "nonaktif";
    if (hatching.includes(a.id)) return "baru menetas";
    return "online · idle";
  }
</script>

<svelte:window onkeydown={onKey} />

<ToastHost />

<div class="relative flex h-full w-full overflow-hidden bg-white-100 dark:bg-navy-800">
  <!-- Roster -->
  {#if rosterOpen}
    <button
      type="button"
      class="lg:hidden fixed inset-0 z-30 bg-navy-900/40"
      aria-label="Tutup daftar agent"
      onclick={() => (rosterOpen = false)}
    ></button>
  {/if}
  <aside
    class="{rosterOpen ? 'flex' : 'hidden'} lg:flex fixed lg:sticky inset-y-0 left-0 z-40 w-[300px] shrink-0 flex-col border-r border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700"
  >
    <div class="flex items-center gap-1.5 px-3 pt-3.5 pb-2.5">
      <a
        href="{base}/sessions"
        class="flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600"
        title="Kembali ke wick"
        aria-label="Kembali ke wick"
      >
        <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 4L2 8l4 4"></path><path d="M2 8h8a4 4 0 014 4v1"></path></svg>
      </a>
      <h1 class="flex-1 text-base font-bold text-black-900 dark:text-white-100">Team</h1>
      <button
        type="button"
        class="new-agent flex h-8 w-8 items-center justify-center rounded-full bg-green-500 text-lg leading-none text-white-100 hover:bg-green-600"
        title="+ Agent"
        aria-label="Agent baru"
        onclick={() => { rosterOpen = false; openPanel({ kind: "new" }); }}
      >+</button>
    </div>
    <label class="mx-3 mb-2 flex items-center gap-2 rounded-xl border border-transparent bg-white-300 px-3 py-2 focus-within:border-green-500 dark:bg-navy-600">
      <svg class="h-4 w-4 shrink-0 text-black-700" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round"><circle cx="7" cy="7" r="4.5"></circle><path d="M10.5 10.5L14 14"></path></svg>
      <input
        type="search"
        bind:value={query}
        placeholder="Search"
        aria-label="Cari agent"
        class="min-w-0 flex-1 border-0 bg-transparent p-0 text-sm text-black-900 placeholder:text-black-700 focus:outline-none dark:text-white-100"
      />
    </label>
    <nav class="flex-1 overflow-y-auto px-2 pb-4" aria-label="Daftar agent">
      {#if !loaded}
        <p class="px-3 py-4 text-sm text-black-800 dark:text-black-600">Memuat…</p>
      {:else if loadError}
        <p class="px-3 py-4 text-sm text-neg-400">{loadError}</p>
      {:else if roster.length === 0}
        <p class="px-3 py-4 text-sm text-black-800 dark:text-black-600">Tidak ada agent yang cocok.</p>
      {/if}
      {#each roster as a (a.id)}
        {@const active = selected?.id === a.id}
        {@const working = isWorking(a.status)}
        <button
          type="button"
          class="roster-row relative mb-0.5 flex w-full items-center gap-3 rounded-2xl px-2.5 py-2.5 text-left {active
            ? 'bg-white-300 dark:bg-navy-600'
            : 'hover:bg-white-300 dark:hover:bg-navy-600'}"
          aria-current={active ? "page" : undefined}
          onclick={() => openAgent(a)}
        >
          <AgentAvatar shape={a.avatar?.shape} color={a.avatar?.color} size={44} {working} asleep={a.disabled} hatching={hatching.includes(a.id)} />
          <span class="roster-tip rounded-lg bg-black-900 px-2 py-0.5 text-[11px] text-white-100 shadow-md">{rowTip(a)}</span>
          <span class="min-w-0 flex-1">
            <span class="flex items-baseline gap-2">
              <span class="roster-name min-w-0 flex-1 truncate font-semibold text-black-900 dark:text-white-100">
                {a.name}{#if a.is_captain}<span class="ml-1.5 align-middle text-[9px] font-bold tracking-wider text-green-600 dark:text-green-400">★ CAPTAIN</span>{/if}
              </span>
              <span class="shrink-0 text-xs text-black-700">{rosterTime(a.last_active)}</span>
            </span>
            <span class="mt-0.5 block truncate text-[13px] {working ? 'font-medium text-green-600 dark:text-green-400' : 'text-black-800 dark:text-black-600'}">
              {#if working}Typing<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>{:else}{rowPreview(a)}{/if}
            </span>
          </span>
        </button>
      {/each}
    </nav>
    <div class="flex items-center gap-2.5 border-t border-white-300 px-3 py-2.5 dark:border-navy-600">
      <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-green-200 text-xs font-bold text-green-700 select-none">{viewerInitial}</span>
      <span class="min-w-0 flex-1 truncate text-[13px] font-medium text-black-900 dark:text-white-100">{viewerName || "Kamu"}</span>
      <span class="shrink-0 text-xs text-black-700">{agents.length} agent</span>
    </div>
  </aside>

  <!-- Chat -->
  <section class="flex min-w-0 flex-1 flex-col">
    <header class="flex h-16 shrink-0 items-center gap-3 border-b border-white-300 px-4 dark:border-navy-600">
      <button
        type="button"
        class="lg:hidden flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600"
        aria-label="Daftar agent"
        onclick={() => (rosterOpen = true)}
      >
        <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M2 4h12M2 8h12M2 12h12"></path></svg>
      </button>
      {#if selected}
        <AgentAvatar shape={selected.avatar?.shape} color={selected.avatar?.color} size={36} working={isWorking(selected.status)} asleep={selected.disabled} hatching={hatching.includes(selected.id)} />
        <div class="min-w-0 flex-1">
          <div class="truncate text-base font-semibold text-black-900 dark:text-white-100">
            {selected.name}
          </div>
          <div class="truncate text-xs text-black-800 dark:text-black-600">
            {#if isWorking(selected.status)}
              <span class="font-medium text-green-600 dark:text-green-400">mengetik<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span></span>
            {:else if selected.disabled}
              nonaktif
            {:else}
              <span class="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-green-500 align-middle"></span>online
            {/if}
            · @{selected.handle}{route.session ? " · percakapan lain" : ""}
          </div>
        </div>
        {#if route.session}
          <button
            type="button"
            class="rounded-lg px-2 py-1 text-xs font-medium text-link-400 hover:bg-white-200 dark:hover:bg-navy-700"
            onclick={() => go({ session: null })}
          >Chat utama</button>
        {/if}
        <KebabMenu items={menuItems} ariaLabel="Menu agent" width={192} />
      {:else}
        <div class="flex-1"></div>
      {/if}
    </header>
    <div class="min-h-0 flex-1">
      {#if selected && chatSessionId}
        {#key chatSessionId}
          <DetailView {base} sessionId={chatSessionId} {agentMode} />
        {/key}
      {:else if loaded && !loadError && selected}
        <div class="flex h-full items-center justify-center text-sm text-black-800 dark:text-black-600">Membuka chat…</div>
      {:else if loaded && !loadError && agents.length === 0}
        <div class="flex h-full items-center justify-center text-sm text-black-800 dark:text-black-600">Belum ada agent.</div>
      {/if}
    </div>
  </section>

  <!-- Drawer (Settings / Percakapan lain / + Agent) -->
  {#if route.panel}
    <button
      type="button"
      class="fixed inset-0 z-40 bg-navy-900/40"
      aria-label="Tutup panel"
      onclick={() => openPanel(null)}
    ></button>
    <div
      class="fixed inset-y-0 right-0 z-50 flex w-full max-w-xl flex-col bg-white-100 shadow-xl dark:bg-navy-700 sm:m-2 sm:rounded-xl"
      role="dialog"
      aria-modal="true"
    >
      {#if route.panel.kind === "new"}
        <AgentWizard {base} onClose={() => openPanel(null)} {onCreated} />
      {:else if selected && route.panel.kind === "settings"}
        <AgentSettings
          {base}
          agent={selected}
          {agents}
          tab={route.panel.tab}
          onTab={(tab) => go({ panel: { kind: "settings", tab } }, true)}
          onClose={() => openPanel(null)}
          {onSaved}
          onDeleted={() => onDeleted(selected.id)}
        />
      {:else if selected && route.panel.kind === "sessions"}
        <AgentSessions
          {base}
          agent={selected}
          current={chatSessionId}
          onClose={() => openPanel(null)}
          onPick={(id, main) => go({ session: main ? null : id, panel: null })}
          onNew={newChat}
        />
      {/if}
    </div>
  {/if}
</div>

<style>
  /* Roster bits the token scale has no exact step for (mockup sizes). */
  .roster-name { font-size: 15px; }
  .new-agent { box-shadow: 0 4px 12px rgba(39, 177, 153, 0.35); }
  .roster-tip {
    display: none;
    position: absolute;
    left: 60px;
    top: -4px;
    z-index: 30;
    white-space: nowrap;
    pointer-events: none;
  }
  .roster-row:hover .roster-tip { display: block; }
  /* Three dots that bounce in turn: the "typing" cue in the header and the
     roster. Tailwind has no staggered keyframe, hence the local rule. */
  .dots i {
    display: inline-block;
    width: 4px;
    height: 4px;
    margin-left: 2px;
    border-radius: 9999px;
    background: currentColor;
    animation: agent-dot 1s infinite;
  }
  .dots i:nth-child(2) { animation-delay: 0.15s; }
  .dots i:nth-child(3) { animation-delay: 0.3s; }
  @keyframes agent-dot {
    0%, 60%, 100% { opacity: 0.3; transform: translateY(0); }
    30% { opacity: 1; transform: translateY(-2px); }
  }
  @media (prefers-reduced-motion: reduce) {
    .dots i { animation: none; opacity: 0.7; }
  }
</style>
