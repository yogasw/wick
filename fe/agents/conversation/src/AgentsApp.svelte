<script lang="ts">
  /* The Team app (/team; "Agents" in code): a messaging-style roster on the left and the
     chosen agent's chat on the right. The chat IS the ordinary
     conversation view (DetailView) in agent mode, so streaming, approvals,
     artifacts and the rail all come along instead of being rebuilt.
     Settings, other conversations and the + Agent wizard are drawers with
     their own URL (agentsRouter.ts), so they survive a refresh. */
  import { onMount, untrack } from "svelte";
  import { KebabMenu, ToastHost } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import DetailView from "./lib/components/DetailView.svelte";
  import { AgentAvatar, setIdleAnimations } from "@wick-fe/common-avatar";
  import AgentSettings from "./lib/components/AgentSettings.svelte";
  import SharedAgentInfo from "./lib/components/SharedAgentInfo.svelte";
  import { agentMenu, isSharedAgent, sharedChatMode, sharedLabel } from "./lib/agentSharing.js";
  import AgentWizard from "./lib/components/AgentWizard.svelte";
  import RemoteAgentWizard from "./lib/components/team/RemoteAgentWizard.svelte";
  import SlackRemoteWizard from "./lib/components/team/SlackRemoteWizard.svelte";
  import PluginRemoteWizard from "./lib/components/team/PluginRemoteWizard.svelte";
  import RemoteQuestionCard from "./lib/components/team/RemoteQuestionCard.svelte";
  import { isA2ARemote, isRemoteAgent, isSlackRemote, remoteBadge, remoteChatMode, remoteSubtitle } from "./lib/remoteAgent.js";
  import AgentSessions from "./lib/components/AgentSessions.svelte";
  import AgentConnections from "./lib/components/AgentConnections.svelte";
  import AgentScheduled from "./lib/components/AgentScheduled.svelte";
  import { agentsRoute, navigate, type AgentsRoute, type AgentsPanel } from "./lib/agentsRouter.js";
  import { connectorCaption, hiddenTabsFor } from "./lib/agentMode.js";
  import { nativeToolsOf } from "./lib/nativeTools.js";
  import { rosterTime } from "./lib/timeFormat.js";
  import { listAgents, openAgentChat, createAgent, updateAgent, markAgentRead, runApi, isWorking, type AgentItem } from "./lib/api/team.js";
  import { listGroups, getTeamSettings, type GroupItem } from "./lib/api/team.js";
  import GroupView from "./lib/components/GroupView.svelte";
  import NewGroupDialog from "./lib/components/NewGroupDialog.svelte";
  import GroupSettings from "./lib/components/GroupSettings.svelte";
  import GroupAvatars from "./lib/components/GroupAvatars.svelte";
  import { rosterStatus, withTurn } from "./lib/rosterStatus.js";
  import { duplicateBody } from "./lib/agentDuplicate.js";
  import TeamAccountMenu from "./lib/components/TeamAccountMenu.svelte";
  import TeamAddMenu from "./lib/components/TeamAddMenu.svelte";
  import { rosterEntries, mobilePins, canMakeGroup, unreadLabel } from "./lib/rosterList.js";
  import TeamSettings from "./lib/components/TeamSettings.svelte";
  import { RETURN_KEY, returnHref, classicHref } from "./lib/teamReturn.js";

  const appEl = document.getElementById("app");
  const base = appEl?.dataset.base ?? "";
  const viewerName = appEl?.dataset.viewerName ?? "";
  const themeMode = appEl?.dataset.themeMode;
  const theme =
    themeMode === "light" || themeMode === "dark"
      ? { mode: themeMode, light: appEl?.dataset.themeLight ?? "", dark: appEl?.dataset.themeDark ?? "" }
      : null;

  /* The account menu's "Switch to Agents" goes back to the wick page the user entered
     from. The stored page is read once and dropped, so a later entry from
     elsewhere (the Overview card) is not sent to a stale one; a reload
     keeps document.referrer. classicHref keeps a landing target from
     redirecting straight back here ("Open Team when I open Agents"). */
  const exitHref = (() => {
    let stored: string | null = null;
    try {
      stored = sessionStorage.getItem(RETURN_KEY);
      sessionStorage.removeItem(RETURN_KEY);
    } catch {
      // storage blocked: referrer only
    }
    return classicHref(returnHref(stored, document.referrer, location.origin, base), base);
  })();

  let route = $state<AgentsRoute>({ handle: null, session: null, panel: null });
  agentsRoute.subscribe((v) => { route = v; });

  let agents = $state<AgentItem[]>([]);
  let captainId = $state("");
  let loaded = $state(false);
  let loadError = $state("");
  let query = $state("");
  // Below lg the roster is a drawer over the chat, opened from the header.
  let rosterOpen = $state(false);

  /* Group chats: listed under the agents; the open one (/team/g/<id>)
     replaces the agent chat. */
  let groups = $state<GroupItem[]>([]);
  let groupsLoaded = $state(false);
  const activeGroupId = $derived(route.group ?? "");
  const groupSettingsOpen = $derived(route.panel?.kind === "group-settings");
  let newGroupOpen = $state(false);
  /* + Agent's type: a wick agent, or a remote one by source (A2A or
     Slack), each with its own wizard. */
  let newType = $state<"local" | "remote" | "slack" | "plugin">("local");
  const activeGroup = $derived(groups.find((g) => g.id === activeGroupId));
  async function loadGroups() {
    try {
      groups = (await runApi(listGroups(base))).groups ?? [];
    } catch {
      /* an older server has no groups */
    } finally {
      groupsLoaded = true;
    }
  }

  /* The last roster this tab read, shown at once on the next open so the
     selected agent's chat (main_session_id) loads in parallel with the
     roster instead of after it. `loaded` still waits for the server, so a
     stale cache never sends an unknown handle away. */
  const ROSTER_CACHE = "wick.team.roster";
  try {
    const c = JSON.parse(sessionStorage.getItem(ROSTER_CACHE) ?? "null") as { agents?: AgentItem[]; captain_id?: string } | null;
    if (c?.agents?.length) {
      agents = c.agents;
      captainId = c.captain_id ?? "";
    }
  } catch {
    /* no cache */
  }

  /* One roster read at a time: the poll, a finished turn and a save can
     all ask at once, and they share the request in flight. */
  let inflight: Promise<void> | null = null;
  function load(): Promise<void> {
    inflight ??= (async () => {
      try {
        const r = await runApi(listAgents(base));
        agents = r.agents ?? [];
        captainId = r.captain_id ?? "";
        loadError = "";
        try {
          sessionStorage.setItem(ROSTER_CACHE, JSON.stringify({ agents, captain_id: captainId }));
        } catch {
          /* storage full or off */
        }
      } catch (e) {
        loadError = e instanceof Error ? e.message : String(e);
      } finally {
        loaded = true;
        inflight = null;
      }
    })();
    return inflight;
  }

  // Status dots go stale without a refresh. The open chat's own turns
  // already refresh it (onTurnChange), so the poll only catches the rest.
  /* Team settings › Idle animations applies to every avatar on the page;
     the drawer updates it on save. A failed read keeps the default (on). */
  async function loadIdleAnimations() {
    try {
      setIdleAnimations((await runApi(getTeamSettings(base))).idle_animations !== false);
    } catch {
      /* keep the default */
    }
  }

  onMount(() => {
    load();
    loadGroups();
    void loadIdleAnimations();
    const t = setInterval(() => {
      if (document.visibilityState === "visible") { load(); loadGroups(); }
    }, 30000);
    return () => clearInterval(t);
  });

  const captain = $derived(agents.find((a) => a.id === captainId) ?? agents.find((a) => a.is_captain));

  /* Agents and groups in one list, newest activity first. The mobile
     drawer's pins (shown only while not searching) leave the list there. */
  const entries = $derived(rosterEntries(agents, groups, query));
  const pins = $derived(query.trim() ? [] : mobilePins(agents));
  const pinIds = $derived(new Set(pins.map((a) => a.id)));

  const selected = $derived(
    route.handle ? agents.find((a) => a.handle === route.handle) : captain,
  );

  /* /team names no agent: settle on the Captain, rewriting the URL rather
     than adding a history entry. An unknown handle does the same, once the
     list has actually loaded. */
  $effect(() => {
    if (!loaded || loadError) return;
    if (route.handle && !agents.some((a) => a.handle === route.handle)) {
      toastError(`Agent @${route.handle} not found`);
      navigate({ handle: captain?.handle ?? null, session: null, panel: route.panel }, { replace: true });
      return;
    }
    if (route.group) {
      if (groupsLoaded && !groups.some((g) => g.id === route.group)) {
        toastError("Group not found");
        navigate({ handle: captain?.handle ?? null, session: null, panel: null }, { replace: true });
      }
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
      .catch((e) => toastError(`Open chat: ${e instanceof Error ? e.message : String(e)}`))
      .finally(() => { opening = null; });
  });

  /* Opening an agent's chat reads it; leaving it reads it again, since the
     owner's own messages and the replies they watched arrive while it is
     open. The dot drops locally at once; the POST is best-effort. */
  let readId = "";
  function markRead(id: string) {
    agents = agents.map((x) => (x.id === id && x.unread ? { ...x, unread: false } : x));
    runApi(markAgentRead(base, id)).catch(() => {});
  }
  $effect(() => {
    const id = selected?.id ?? "";
    if (id === readId) return;
    untrack(() => {
      if (readId) markRead(readId);
      readId = id;
      if (id) markRead(id);
    });
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
  function newAgent() {
    rosterOpen = false;
    newType = "local";
    openPanel({ kind: "new" });
  }

  async function newChat() {
    if (!selected) return;
    try {
      const r = await runApi(openAgentChat(base, selected.id, true));
      go({ session: r.session_id, panel: null });
    } catch (e) {
      toastError(`New chat: ${e instanceof Error ? e.message : String(e)}`);
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

  /* A copy gets its own project holding a copy of the persona, plus the same
     look and access, under the next free @handle. It then hatches. */
  async function duplicate() {
    if (!selected) return;
    try {
      const a = await runApi(createAgent(base, duplicateBody(selected, agents.map((x) => x.handle))));
      toastOk(`Agent @${a.handle} created`);
      onCreated(a);
    } catch (e) {
      toastError(`Duplicate: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function toggleDisabled() {
    if (!selected) return;
    const off = !selected.disabled;
    try {
      onSaved(await runApi(updateAgent(base, selected.id, { disabled: off })));
      toastOk(off ? `@${selected.handle} disabled` : `@${selected.handle} enabled again`);
    } catch (e) {
      toastError(`${off ? "Disable" : "Enable"}: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  /* Same order as the mockup: where to go first, then what to do to the
     agent. A new chat lives in the Chats drawer, not here. */
  const fullMenu = $derived([
    { label: "Chats", hint: "main chat and history", onclick: () => openPanel({ kind: "sessions" }) },
    selected && isRemoteAgent(selected)
      ? { label: "Settings", hint: isSlackRemote(selected) ? "remote Slack, mention, avatar" : "remote A2A, mention, avatar", onclick: () => openPanel({ kind: "settings", tab: "remote" }) }
      : { label: "Settings", hint: "persona, access, tools, avatar", onclick: () => openPanel({ kind: "settings", tab: "persona" }) },
    { label: "Connections", hint: "Slack and health", onclick: () => openPanel({ kind: "connections" }) },
    { label: "Scheduled", hint: "work it runs on a schedule", onclick: () => openPanel({ kind: "scheduled" }) },
    // A copy of a remote agent would be a local one with no persona.
    ...(selected && isRemoteAgent(selected) ? [] : [{ label: "Duplicate agent", hint: "copies persona & access, not connections", divider: true, onclick: duplicate }]),
    selected?.disabled
      ? { label: "Enable", hint: "the agent can be used again", onclick: toggleDisabled }
      : { label: "Disable", hint: "closes all connector access", danger: true, onclick: toggleDisabled },
  ]);
  /* An agent shared with the user: chat and a read-only info card only. */
  const menuItems = $derived(
    agentMenu(selected, fullMenu, { label: "Info", hint: selected ? sharedLabel(selected) : "", onclick: () => openPanel({ kind: "settings", tab: "persona" }) }),
  );

  /* The main chat's turn started or ended on the stream: flip the row and
     header now, and re-read the roster once it ends so the preview and
     status match the server. Another conversation on screen is not the
     main chat, so it leaves the row alone. */
  function onTurnChange(active: boolean) {
    const a = selected;
    if (!a || route.session) return;
    agents = withTurn(agents, a.id, active);
    if (!active) load();
  }

  /* The header's panel button drives DetailView's rail: a press bumps the
     count, DetailView reports back whether the rail is open. */
  let railToggle = $state(0);
  let railOpen = $state(false);

  // An A2A remote agent has no local process: no rail, its own caption.
  const remoteMode = $derived(selected && isRemoteAgent(selected) ? remoteChatMode(selected) : null);
  // A shared agent's rail is its owner's project: chat only.
  const sharedMode = $derived(sharedChatMode(selected));
  const agentMode = $derived({
    hideTabs: sharedMode?.hideTabs ?? remoteMode?.hideTabs ?? hiddenTabsFor(selected?.features, selected ? nativeToolsOf(selected.allowed_native_tools) : null),
    ...(sharedMode ? { railNote: sharedMode.railNote, chatOnly: true } : remoteMode ? { railNote: remoteMode.railNote } : {}),
    hideHeader: true,
    hidePickers: true,
    onDeleted: () => go({ session: null }),
    onTurnChange,
    providerSwitch: !!selected?.allow_provider_switch,
    onOpenSettings: () => openPanel({ kind: "settings", tab: "advanced" }),
    onNewChat: newChat,
    agent: selected
      ? {
          id: selected.id,
          handle: selected.handle,
          name: selected.name,
          tagline: selected.tagline,
          description: selected.description,
          kind: selected.avatar?.kind,
          shape: selected.avatar?.shape,
          color: selected.avatar?.color,
          expression: selected.avatar?.expression,
          caption: remoteMode?.caption ?? connectorCaption(selected.allowed_connectors),
        }
      : undefined,
  });

  function rowPreview(a: AgentItem): string {
    if (a.disabled) return "Disabled";
    // A shared agent the user has not chatted with yet says whose it is.
    return a.attention_preview || a.last_preview || (isSharedAgent(a) ? sharedLabel(a) : "") || a.description || "No chats yet";
  }

</script>

<svelte:window onkeydown={onKey} />

<ToastHost />

<div class="team-app relative flex h-full w-full overflow-hidden bg-white-100 dark:bg-navy-800">
  <!-- Roster -->
  {#if rosterOpen}
    <button
      type="button"
      class="lg:hidden fixed inset-0 z-30 bg-navy-900/40"
      aria-label="Close agent list"
      onclick={() => (rosterOpen = false)}
    ></button>
  {/if}
  <aside
    class="{rosterOpen ? 'flex' : 'hidden'} lg:flex fixed lg:sticky inset-y-0 left-0 z-40 w-[300px] shrink-0 flex-col border-r border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700"
  >
    <!-- No title bar (team-sidebar mockup, final): Search and the grey +
         on top, the list, then the account row at the foot. -->
    <div class="flex items-center gap-1.5 px-2.5 pb-2 pt-3">
      <label class="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-xl border border-transparent bg-white-300 px-3 focus-within:border-green-500 dark:bg-navy-600">
        <svg class="h-4 w-4 shrink-0 text-black-700" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round"><circle cx="7" cy="7" r="4.5"></circle><path d="M10.5 10.5L14 14"></path></svg>
        <input
          type="search"
          bind:value={query}
          placeholder="Search"
          aria-label="Search agents"
          class="min-w-0 flex-1 border-0 bg-transparent p-0 text-sm text-black-900 placeholder:text-black-700 focus:outline-none dark:text-white-100"
        />
      </label>
      <TeamAddMenu canGroup={canMakeGroup(agents)} onAgent={newAgent} onGroup={() => { rosterOpen = false; newGroupOpen = true; }} />
    </div>
    <!-- Mobile drawer only: three big agents to jump to; the list below
         then skips them (lg shows every row, pins hidden). -->
    {#if pins.length}
      <div class="flex justify-center gap-2 px-2 pb-3 pt-1 lg:hidden" data-testid="roster-pins">
        {#each pins as a (a.id)}
          <button type="button" class="flex w-20 flex-col items-center gap-1.5 rounded-xl py-1 text-xs text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600" onclick={() => openAgent(a)}>
            <span class="relative inline-flex">
              <AgentAvatar kind={a.avatar?.kind} shape={a.avatar?.shape} expression={a.avatar?.expression} color={a.avatar?.color} size={54} working={isWorking(a.status)} asleep={a.disabled} />
              <!-- Same cue as the list's SHARED badge, sized for a pin. -->
              {#if isSharedAgent(a)}<span class="absolute bottom-0 right-0 flex h-5 w-5 items-center justify-center rounded-full border-2 border-white-100 bg-white-300 text-black-800 dark:border-navy-700 dark:bg-navy-600 dark:text-black-600" title={sharedLabel(a)} data-testid="pin-shared-badge"><svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="18" cy="5" r="3" /><circle cx="6" cy="12" r="3" /><circle cx="18" cy="19" r="3" /><path d="M8.6 13.5l6.8 4M15.4 6.5l-6.8 4" /></svg><span class="sr-only">{sharedLabel(a)}</span></span>{/if}
            </span>
            <span class="w-full truncate text-center">{a.name}</span>
          </button>
        {/each}
      </div>
    {/if}
    <nav class="flex-1 overflow-y-auto px-1.5 pb-2" aria-label="Agent list">
      {#if !loaded}
        <p class="px-3 py-4 text-sm text-black-800 dark:text-black-600">Loading…</p>
      {:else if loadError}
        <p class="px-3 py-4 text-sm text-neg-400">{loadError}</p>
      {:else if agents.length === 0}
        <div class="flex flex-col items-center gap-3 px-4 py-10 text-center" data-testid="roster-empty">
          <p class="text-sm text-black-800 dark:text-black-600">No agents yet.</p>
          <button type="button" class="rounded-xl bg-green-500 px-4 py-2 text-sm font-semibold text-white-100 hover:bg-green-600" onclick={newAgent}>New agent</button>
        </div>
      {:else if entries.length === 0}
        <p class="px-3 py-4 text-sm text-black-800 dark:text-black-600">No matches.</p>
      {/if}
      {#each entries as e (e.kind + e.id)}
        {#if e.kind === "agent"}
          {@const a = e.agent}
          {@const active = !route.group && selected?.id === a.id}
          {@const st = rosterStatus(a, { activeId: selected?.id, hatching: hatching.includes(a.id) })}
          <button
            type="button"
            class="roster-row relative mb-0.5 w-full items-center gap-2.5 rounded-xl px-2 py-2 text-left {pinIds.has(a.id) ? 'hidden lg:flex' : 'flex'} {active
              ? 'bg-white-300 dark:bg-navy-600'
              : 'hover:bg-white-300 dark:hover:bg-navy-600'}"
            aria-current={active ? "page" : undefined}
            data-testid="roster-agent"
            onclick={() => openAgent(a)}
          >
            <AgentAvatar kind={a.avatar?.kind} shape={a.avatar?.shape} expression={a.avatar?.expression} color={a.avatar?.color} size={38} working={isWorking(a.status)} tool={!!a.current_action} asleep={a.disabled} hatching={hatching.includes(a.id)} alert={st.attention} notify={st.unread} />
            <span class="roster-tip rounded-lg bg-black-900 px-2 py-0.5 text-[11px] text-white-100 shadow-md">{st.tip}</span>
            <span class="min-w-0 flex-1">
              <span class="flex items-baseline gap-2">
                <span class="min-w-0 flex-1 truncate text-sm font-semibold text-black-900 dark:text-white-100">
                  {a.name}{#if a.is_captain}<span class="ml-1.5 align-middle text-[10px] font-semibold tracking-wider text-green-600 dark:text-green-400">CAPTAIN</span>{/if}{#if isRemoteAgent(a)}<span class="ml-1.5 rounded-full bg-white-300 px-1.5 py-px align-middle text-[9px] font-bold uppercase tracking-wider text-black-800 dark:bg-navy-600 dark:text-black-600" data-testid="roster-remote-badge">{remoteBadge(a)}</span>{/if}{#if isSharedAgent(a)}<span class="ml-1.5 rounded-full bg-white-300 px-1.5 py-px align-middle text-[9px] font-bold uppercase tracking-wider text-black-800 dark:bg-navy-600 dark:text-black-600" title={sharedLabel(a)} data-testid="roster-shared-badge">Shared</span>{/if}
                </span>
                <span class="shrink-0 text-[11px] text-black-700">{rosterTime(a.last_active)}</span>
              </span>
              <span class="mt-0.5 flex items-center gap-1.5">
                <span class="min-w-0 flex-1 truncate text-xs {st.typing !== null ? 'font-medium text-green-600 dark:text-green-400' : st.attention && a.attention_preview ? 'font-medium text-amber-700 dark:text-amber-300' : 'text-black-800 dark:text-black-600'}">
                  {#if st.typing !== null}typing…{:else}{rowPreview(a)}{/if}
                </span>
                {#if st.unread}<span class="roster-badge shrink-0 rounded-full bg-green-500 text-white-100" aria-label="new message">{unreadLabel(a.unread_count)}</span>{/if}
              </span>
            </span>
          </button>
        {:else}
          {@const g = e.group}
          {@const unread = g.unread && activeGroupId !== g.id}
          <button
            type="button"
            class="roster-row relative mb-0.5 flex w-full items-center gap-2.5 rounded-xl px-2 py-2 text-left {activeGroupId === g.id ? 'bg-white-300 dark:bg-navy-600' : 'hover:bg-white-300 dark:hover:bg-navy-600'}"
            aria-current={activeGroupId === g.id ? "page" : undefined}
            data-testid="roster-group"
            onclick={() => { rosterOpen = false; g.unread = false; navigate({ handle: null, session: null, panel: null, group: g.id }); }}
          >
            <span class="flex shrink-0 items-center justify-center" style="width:38px;height:38px"><GroupAvatars members={g.members} size={17} max={2} showMore={false} ring="bg-white-200 dark:bg-navy-700" /></span>
            <span class="min-w-0 flex-1">
              <span class="flex items-baseline gap-2">
                <span class="min-w-0 flex-1 truncate text-sm font-semibold text-black-900 dark:text-white-100">{g.name}</span>
                <span class="shrink-0 text-[11px] text-black-700">{rosterTime(g.last_active)}</span>
              </span>
              <span class="mt-0.5 flex items-center gap-1.5">
                <span class="min-w-0 flex-1 truncate text-xs text-black-800 dark:text-black-600">{g.last_preview || `${g.members.length} agents`}</span>
                {#if unread}<span class="roster-badge shrink-0 rounded-full bg-green-500 text-white-100" aria-label="new message">{unreadLabel(g.unread_count)}</span>{/if}
              </span>
            </span>
          </button>
        {/if}
      {/each}
    </nav>
    <div class="border-t border-white-300 px-1.5 pb-2.5 pt-1.5 dark:border-navy-600" data-testid="roster-account">
      <TeamAccountMenu
        {viewerName}
        {exitHref}
        {theme}
        onSettings={() => { rosterOpen = false; openPanel({ kind: "team-settings", tab: "general" }); }}
      />
    </div>
  </aside>

  <!-- Chat -->
  {#if activeGroup}
  <section class="flex min-w-0 flex-1 flex-col">
    {#key activeGroup.id}
      <GroupView
        {base}
        group={activeGroup}
        {agents}
        onMenu={() => (rosterOpen = true)}
        onSettings={() => openPanel({ kind: "group-settings" })}
      />
    {/key}
  </section>
  {:else}
  <section class="flex min-w-0 flex-1 flex-col">
    <header class="flex h-16 shrink-0 items-center gap-3 border-b border-white-300 px-4 dark:border-navy-600">
      <button
        type="button"
        class="lg:hidden flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600"
        aria-label="Agent list"
        onclick={() => (rosterOpen = true)}
      >
        <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M2 4h12M2 8h12M2 12h12"></path></svg>
      </button>
      {#if selected}
        <AgentAvatar kind={selected.avatar?.kind} shape={selected.avatar?.shape} expression={selected.avatar?.expression} color={selected.avatar?.color} size={36} live working={isWorking(selected.status)} tool={!!selected.current_action} asleep={selected.disabled} hatching={hatching.includes(selected.id)} />
        <div class="min-w-0 flex-1">
          <div class="truncate text-base font-semibold text-black-900 dark:text-white-100">
            {selected.name}{#if selected.tagline}<span class="font-normal text-black-700 dark:text-black-600">&nbsp;·&nbsp;{selected.tagline}</span>{/if}
          </div>
          <div class="truncate text-xs text-black-800 dark:text-black-600">
            {#if isWorking(selected.status)}
              <span class="font-medium text-green-600 dark:text-green-400">typing<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span></span>
            {:else if selected.disabled}
              disabled
            {:else}
              <span class="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-green-500 align-middle"></span>online
            {/if}
            · @{selected.handle}{route.session ? " · other chat" : ""}{#if isRemoteAgent(selected)}<span data-testid="header-remote"> · {remoteSubtitle(selected)}</span>{/if}
          </div>
        </div>
        <!-- Always-on switcher: names the conversation on screen and opens
             the list of the others (main chat first). -->
        <button
          type="button"
          class="flex shrink-0 items-center gap-1.5 rounded-full border border-white-300 px-3 py-1.5 text-xs font-medium text-black-900 hover:bg-white-200 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700"
          title="Switch chat"
          aria-haspopup="dialog"
          onclick={() => openPanel({ kind: "sessions" })}
        >
          {#if route.session}
            <svg class="h-3.5 w-3.5 shrink-0 text-black-700" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z"></path></svg>
            <span class="hidden sm:inline">Other chat</span>
          {:else}
            <svg class="h-3.5 w-3.5 shrink-0 text-green-600 dark:text-green-400" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 2.5h4M7 2.5v4L4.5 9h7L9 6.5v-4M8 9v4.5"></path></svg>
            <span class="hidden sm:inline">Main chat</span>
          {/if}
          <svg class="h-3 w-3 shrink-0 text-black-700" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 6l4 4 4-4"></path></svg>
        </button>
        <KebabMenu items={menuItems} ariaLabel="Agent menu" width={240} />
        {#if !remoteMode}
        <button
          type="button"
          class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600 {railOpen ? 'bg-white-300 dark:bg-navy-600' : ''}"
          title="Panel"
          aria-label="Panel"
          aria-pressed={railOpen}
          data-rail-toggle
          onclick={() => railToggle++}
        >
          <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="2.5" width="12" height="11" rx="2"></rect><path d="M10 2.5v11"></path></svg>
        </button>
        {/if}
      {:else}
        <div class="flex-1"></div>
      {/if}
    </header>
    {#if selected && chatSessionId && remoteMode && isA2ARemote(selected)}
      <RemoteQuestionCard
        {base}
        agentId={selected.id}
        sessionId={chatSessionId}
        handle={selected.handle}
        turn={`${selected.status}|${selected.last_active ?? ""}`}
        question={route.session ? "" : selected.last_preview}
      />
    {/if}
    <div class="min-h-0 flex-1">
      {#if selected && chatSessionId}
        {#key chatSessionId}
          <DetailView {base} sessionId={chatSessionId} {agentMode} {railToggle} onRailChange={(open) => (railOpen = open)} />
        {/key}
      {:else if loaded && !loadError && selected}
        <div class="flex h-full items-center justify-center text-sm text-black-800 dark:text-black-600">Opening chat…</div>
      {:else if loaded && !loadError && agents.length === 0}
        <div class="flex h-full items-center justify-center text-sm text-black-800 dark:text-black-600">No agents yet.</div>
      {/if}
    </div>
  </section>
  {/if}

  {#if newGroupOpen}
    <NewGroupDialog {base} {agents} onClose={() => (newGroupOpen = false)} onCreated={(g) => { newGroupOpen = false; groups = [g, ...groups]; navigate({ handle: null, session: null, panel: null, group: g.id }); }} />
  {/if}

  {#if groupSettingsOpen && activeGroup}
    <button type="button" class="agent-scrim fixed inset-0 z-40" aria-label="Close panel" onclick={() => openPanel(null)}></button>
    <div class="agent-drawer fixed z-50 flex flex-col overflow-hidden border border-white-300 bg-white-100 shadow-2xl dark:border-navy-600 dark:bg-navy-700" role="dialog" aria-modal="true">
      <GroupSettings
        {base}
        group={activeGroup}
        {agents}
        onClose={() => openPanel(null)}
        onChanged={(g) => (groups = groups.map((x) => (x.id === g.id ? g : x)))}
        onDeleted={() => { const id = activeGroupId; groups = groups.filter((x) => x.id !== id); navigate({ handle: captain?.handle ?? null, session: null, panel: null }, { replace: true }); toastOk("Group deleted"); }}
      />
    </div>
  {/if}

  <!-- Drawer (Settings / Team settings / Other chats) or the centred + Agent modal -->
  {#if route.panel && route.panel.kind !== "group-settings"}
    <button
      type="button"
      class="agent-scrim fixed inset-0 z-40"
      aria-label="Close panel"
      onclick={() => openPanel(null)}
    ></button>
    <div
      class="{route.panel.kind === 'new' ? 'agent-modal' : 'agent-drawer'} fixed z-50 flex flex-col overflow-hidden border border-white-300 bg-white-100 shadow-2xl dark:border-navy-600 dark:bg-navy-700"
      role="dialog"
      aria-modal="true"
    >
      {#if route.panel.kind === "team-settings"}
        <TeamSettings
          {base}
          tab={route.panel.tab}
          onTab={(tab) => go({ panel: { kind: "team-settings", tab } }, true)}
          onClose={() => openPanel(null)}
        />
      {:else if route.panel.kind === "new"}
        {#if newType === "slack" && !route.panel.project}
          <SlackRemoteWizard {base} taken={agents.map((a) => a.handle)} onClose={() => openPanel(null)} {onCreated} onType={(t) => (newType = t)} />
        {:else if newType === "plugin" && !route.panel.project}
          <PluginRemoteWizard {base} taken={agents.map((a) => a.handle)} onClose={() => openPanel(null)} {onCreated} onType={(t) => (newType = t)} />
        {:else if newType === "remote" && !route.panel.project}
          <RemoteAgentWizard {base} taken={agents.map((a) => a.handle)} onClose={() => openPanel(null)} {onCreated} onType={(t) => (newType = t)} />
        {:else}
          <AgentWizard {base} taken={agents.map((a) => a.handle)} convertProject={route.panel.project} onClose={() => openPanel(null)} {onCreated} onType={(t) => (newType = t)} />
        {/if}
      {:else if selected && isSharedAgent(selected) && route.panel.kind !== "sessions"}
        <SharedAgentInfo agent={selected} onClose={() => openPanel(null)} />
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
      {:else if selected && route.panel.kind === "connections"}
        <AgentConnections {base} agent={selected} onClose={() => openPanel(null)} />
      {:else if selected && route.panel.kind === "scheduled"}
        <AgentScheduled
          {base}
          agent={selected}
          onClose={() => openPanel(null)}
          onOpenTools={() => openPanel({ kind: "settings", tab: "tools" })}
          onOpenSession={(id) => go({ session: id === selected.main_session_id ? null : id, panel: null })}
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
  /* Entering from wick is a full page load; a short fade makes it feel like
     opening an app rather than a blank flash. */
  .team-app { animation: agent-fade 0.25s ease-out; }
  /* Roster bits the token scale has no exact step for (mockup sizes):
     the green unread count pill. */
  .roster-badge {
    display: grid;
    place-items: center;
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    font-size: 10px;
    font-weight: 600;
    line-height: 1;
  }
  .roster-tip {
    display: none;
    position: absolute;
    left: 52px;
    top: -6px;
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
  /* Floating drawer (mockup .drawer/.scrim): inset 8px on three sides, 20px
     corners, a short slide-in. Width and blur have no token step. */
  .agent-drawer {
    top: 8px;
    right: 8px;
    bottom: 8px;
    width: min(540px, calc(100% - 16px));
    border-radius: 20px;
    animation: agent-drawer-in 0.28s cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  /* + Agent wizard (mockup .modal): centred, capped at the viewport. */
  .agent-modal {
    top: 50%;
    left: 50%;
    width: min(620px, calc(100% - 24px));
    max-height: calc(100% - 48px);
    border-radius: 20px;
    transform: translate(-50%, -50%);
    animation: agent-modal-in 0.22s cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  @keyframes agent-modal-in {
    from { opacity: 0; transform: translate(-50%, calc(-50% + 12px)) scale(0.98); }
  }
  .agent-scrim {
    background: rgb(10 12 16 / 0.28);
    backdrop-filter: blur(1.5px);
    animation: agent-fade 0.2s;
  }
  @keyframes agent-drawer-in {
    from { opacity: 0.4; transform: translateX(40px); }
    to { opacity: 1; transform: none; }
  }
  @keyframes agent-fade {
    from { opacity: 0; }
  }
  @media (prefers-reduced-motion: reduce) {
    .dots i { animation: none; opacity: 0.7; }
    .team-app, .agent-drawer, .agent-modal, .agent-scrim { animation: none; }
  }
</style>
