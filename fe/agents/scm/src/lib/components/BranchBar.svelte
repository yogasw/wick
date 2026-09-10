<script lang="ts">
  import type { BranchInfo, GitConnectorsResponse } from "$lib/api/scm";
  import {
    listBranches,
    switchBranch,
    createBranch,
    push,
    pull,
    gitConnectors,
    rememberGitConnector,
  } from "$lib/git-actions";
  import GitConnectorModal from "$lib/components/GitConnectorModal.svelte";
  import { activeRepo } from "$lib/stores/scm";

  type Props = { branch: BranchInfo; busy: boolean };
  let { branch, busy }: Props = $props();

  let open = $state(false);
  let locals = $state<string[]>([]);
  let remotes = $state<string[]>([]);
  let newName = $state("");
  let filter = $state("");

  // Credential picker. A push with no chosen connector opens this first:
  // the panel's own git carries no credentials, and running as whichever
  // connector happens to be there is not a decision to make on the
  // user's behalf.
  let ask = $state<{ action: "push" | "pull"; info: GitConnectorsResponse; error?: string } | null>(null);
  let settings = $state<GitConnectorsResponse | null>(null);

  // What this repo currently pushes through, kept so the key button can
  // show that the answer is NOT the ordinary one. Native git gets no
  // marker: it is what git does anyway, and a dot on every repo would
  // stop meaning anything.
  let current = $state<GitConnectorsResponse | null>(null);
  const viaConnector = $derived(
    !!current?.selected && current.selected !== "native",
  );
  const connectorLabel = $derived(
    current?.candidates.find((c) => c.id === current?.selected)?.label ?? "",
  );

  async function refreshCurrent() {
    current = await gitConnectors();
  }

  // Re-read on repo switch: the choice is per repo, so the marker has to
  // follow the selection rather than the panel's lifetime.
  $effect(() => {
    void $activeRepo;
    void refreshCurrent();
  });

  // Plain git is the default: a repo whose remote needs no credential
  // just works, and nobody is stopped by a dialog to be told so. The
  // picker appears when git ITSELF says the credential is the problem —
  // which is the only moment the answer is worth asking for.
  const AUTH_HINTS = [
    "could not read username",
    "could not read password",
    "authentication failed",
    "terminal prompts disabled",
    "permission denied",
    "403",
    "401",
    "access denied",
    "invalid credentials",
  ];

  function looksLikeAuth(msg: string): boolean {
    const m = msg.toLowerCase();
    return AUTH_HINTS.some((h) => m.includes(h));
  }

  async function run(action: "push" | "pull") {
    const err = await (action === "push" ? push() : pull());
    if (!err) return;
    if (!looksLikeAuth(err)) return;
    // Auth is what failed, so offer the credentials there are.
    const info = await gitConnectors();
    if (!info || info.candidates.length === 0) return;
    ask = { action, info, error: err };
  }

  // The choice always sticks: it is per session and per user, so there is
  // nothing to opt into and no checkbox to explain.
  async function confirmRun(connectorID: string) {
    const action = ask?.action ?? "push";
    ask = null;
    if (connectorID) {
      await rememberGitConnector(connectorID);
      await refreshCurrent();
    }
    await (action === "push" ? push(connectorID) : pull(connectorID));
  }

  async function openSettings() {
    const info = await gitConnectors();
    if (info) settings = info;
  }

  async function saveSettings(connectorID: string) {
    settings = null;
    await rememberGitConnector(connectorID);
    await refreshCurrent();
  }

  async function toggle() {
    open = !open;
    if (open) {
      const bl = await listBranches();
      locals = bl.locals;
      remotes = bl.remotes;
    }
  }
  async function pick(b: string) {
    open = false;
    await switchBranch(b);
  }
  // Checking out a remote branch creates a local tracking branch of the
  // same short name (git does this automatically with `git checkout <name>`).
  async function pickRemote(r: string) {
    open = false;
    const short = r.includes("/") ? r.slice(r.indexOf("/") + 1) : r;
    await switchBranch(short);
  }
  async function create() {
    if (!newName.trim()) return;
    await createBranch(newName);
    newName = "";
    open = false;
  }

  const filteredLocals = $derived(
    filter ? locals.filter((b) => b.toLowerCase().includes(filter.toLowerCase())) : locals,
  );
  const filteredRemotes = $derived(
    filter ? remotes.filter((b) => b.toLowerCase().includes(filter.toLowerCase())) : remotes,
  );
</script>

<div class="border-t border-white-300 dark:border-navy-600 p-3 space-y-2 relative">
  <button
    type="button"
    onclick={toggle}
    class="w-full flex items-center gap-2 rounded-lg border border-white-300 dark:border-navy-600 px-2 py-1.5 text-xs text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
  >
    <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 text-green-500 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
    <span class="min-w-0 flex-1 truncate text-left">{branch.name || "(detached)"}</span>
    {#if branch.ahead > 0}<span class="text-[10px] shrink-0">↑{branch.ahead}</span>{/if}
    {#if branch.behind > 0}<span class="text-[10px] shrink-0">↓{branch.behind}</span>{/if}
  </button>

  {#if open}
    <div class="absolute bottom-full left-3 right-3 mb-1 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-lg p-1.5 z-10 max-h-72 overflow-y-auto">
      <input bind:value={filter} placeholder="Filter branches…" class="mb-1 w-full rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-1.5 py-1 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"/>

      {#if filteredLocals.length > 0}
        <p class="px-1 py-0.5 text-[9px] font-medium uppercase tracking-wide text-black-600 dark:text-black-700">Local</p>
        {#each filteredLocals as b (b)}
          <button type="button" onclick={() => pick(b)} class={"flex w-full items-center gap-1.5 rounded px-2 py-1 text-left text-xs hover:bg-white-200 dark:hover:bg-navy-800 " + (b === branch.name ? "font-semibold text-green-600 dark:text-green-400" : "text-black-800 dark:text-black-600")}>
            {#if b === branch.name}<span class="text-[10px]">✓</span>{:else}<span class="w-2.5"></span>{/if}
            <span class="truncate">{b}</span>
          </button>
        {/each}
      {/if}

      {#if filteredRemotes.length > 0}
        <p class="mt-1 px-1 py-0.5 text-[9px] font-medium uppercase tracking-wide text-black-600 dark:text-black-700">Remote</p>
        {#each filteredRemotes as r (r)}
          <button type="button" onclick={() => pickRemote(r)} class="flex w-full items-center gap-1.5 rounded px-2 py-1 text-left text-xs text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 1a7 7 0 100 14A7 7 0 008 1zM1 8h14M8 1c2 2 2 12 0 14M8 1c-2 2-2 12 0 14" stroke-linecap="round"/></svg>
            <span class="truncate">{r}</span>
          </button>
        {/each}
      {/if}

      <div class="mt-1 flex gap-1 border-t border-white-300 dark:border-navy-600 pt-1.5">
        <input bind:value={newName} placeholder="new branch" class="min-w-0 flex-1 rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-1.5 py-1 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"/>
        <button type="button" onclick={create} title="Create & checkout" class="shrink-0 rounded bg-green-500 px-2 text-xs font-medium text-white-100 hover:bg-green-600">+</button>
      </div>
    </div>
  {/if}

  <div class="flex gap-2">
    <button type="button" onclick={() => run("pull")} disabled={busy} class="flex-1 rounded-lg border border-white-300 dark:border-navy-600 px-2 py-1.5 text-xs text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50 transition-colors">Pull</button>
    <button type="button" onclick={() => run("push")} disabled={busy} class="flex-1 rounded-lg border border-white-300 dark:border-navy-600 px-2 py-1.5 text-xs text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50 transition-colors">Push</button>
    <!-- Change the credential without having to push to be asked. -->
    <button
      type="button"
      onclick={openSettings}
      title={viaConnector
        ? `Pushes through ${connectorLabel}`
        : "Git credential for this session"}
      aria-label="Git credential for this session"
      class="relative shrink-0 rounded-lg border px-2 py-1.5 transition-colors
             {viaConnector
        ? 'border-green-500 text-green-600 dark:text-green-400'
        : 'border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800'}"
    >
      {#if viaConnector}
        <span class="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full bg-green-500"></span>
      {/if}
      <!-- A key, not a gear: this picks the CREDENTIAL a push runs as,
           and a gear next to a theme-toggle-shaped icon reads as settings. -->
      <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="5.5" cy="5.5" r="3"/>
        <path d="M7.7 7.7L13 13M11 11l1.5-1.5M12.5 12.5L14 11" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    </button>
  </div>
</div>

{#if ask}
  <GitConnectorModal
    action={ask.action}
    error={ask.error}
    repo={ask.info.repo}
    remoteURL={ask.info.remote_url}
    candidates={ask.info.candidates}
    selected={ask.info.selected}
    onConfirm={confirmRun}
    onCancel={() => (ask = null)}
  />
{/if}

{#if settings}
  <GitConnectorModal
    repo={settings.repo}
    remoteURL={settings.remote_url}
    candidates={settings.candidates}
    selected={settings.selected}
    onConfirm={(id) => saveSettings(id)}
    onCancel={() => (settings = null)}
  />
{/if}
