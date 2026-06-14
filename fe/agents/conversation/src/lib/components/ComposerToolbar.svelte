<script lang="ts">
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { NOTIFY_KEY } from "../notify-pref.js";
  import type { ProviderOption, ProjectOption } from "../types/agents.js";

  type Props = {
    providers: ProviderOption[];
    projects: ProjectOption[];
    activeProvider: string | null;
    activeProjectId: string | null;
    onProviderChange: (provider: string) => void;
    onProjectChange: (projectId: string | null) => void;
  };

  let {
    providers,
    projects,
    activeProvider,
    activeProjectId,
    onProviderChange,
    onProjectChange,
  }: Props = $props();

  let providerMenuOpen = $state(false);
  let projectMenuOpen = $state(false);
  let notifyOn = $state(
    typeof localStorage !== "undefined" ? localStorage.getItem(NOTIFY_KEY) === "true" : false,
  );
  let bellDenied = $state(
    typeof Notification !== "undefined" ? Notification.permission === "denied" : false,
  );

  const activeProjectName = $derived(
    projects.find((p) => p.id === activeProjectId)?.name ?? "default",
  );

  function toggleProviderMenu() {
    providerMenuOpen = !providerMenuOpen;
    if (providerMenuOpen) projectMenuOpen = false;
  }

  function toggleProjectMenu() {
    projectMenuOpen = !projectMenuOpen;
    if (projectMenuOpen) providerMenuOpen = false;
  }

  function selectProvider(type: string) {
    providerMenuOpen = false;
    onProviderChange(type);
  }

  function selectProject(id: string | null) {
    projectMenuOpen = false;
    onProjectChange(id);
  }

  async function handleBellClick() {
    if (typeof Notification === "undefined") return;

    if (notifyOn) {
      notifyOn = false;
      try { localStorage.setItem(NOTIFY_KEY, "false"); } catch (_) { /* blocked */ }
      toastOk("Notifications muted");
      return;
    }

    if (Notification.permission === "denied") {
      bellDenied = true;
      toastError("Notifications blocked — enable them in your browser settings");
      return;
    }

    if (Notification.permission === "default") {
      const perm = await Notification.requestPermission();
      if (perm === "granted") {
        notifyOn = true;
        bellDenied = false;
        try { localStorage.setItem(NOTIFY_KEY, "true"); } catch (_) { /* blocked */ }
        toastOk("Notifications enabled");
      } else {
        bellDenied = perm === "denied";
        toastError("Notifications blocked — enable them in your browser settings");
      }
      return;
    }

    /* permission === "granted" already */
    notifyOn = true;
    bellDenied = false;
    try { localStorage.setItem(NOTIFY_KEY, "true"); } catch (_) { /* blocked */ }
    toastOk("Notifications enabled");
  }
</script>

<div class="flex items-center gap-1.5">
  <!-- Notification bell -->
  <button
    type="button"
    aria-label="Notifications"
    title={notifyOn ? "Mute notifications" : "Enable notifications"}
    onclick={handleBellClick}
    class="relative inline-flex items-center justify-center h-7 w-7 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600 transition-colors"
  >
    <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
      <path d="M8 2.25c-2.07 0-3.75 1.68-3.75 3.75v2.25L3 9.75v.75h10v-0.75L11.75 8.25V6c0-2.07-1.68-3.75-3.75-3.75z" stroke-linejoin="round"></path>
      <path d="M6.5 12a1.5 1.5 0 0 0 3 0" stroke-linecap="round"></path>
      {#if bellDenied}
        <path d="M3 3l10 10" stroke-linecap="round"></path>
      {/if}
    </svg>
    {#if notifyOn && !bellDenied}
      <span class="absolute -top-0.5 -right-0.5 h-2 w-2 rounded-full bg-green-500 ring-2 ring-white-200 dark:ring-navy-700" aria-hidden="true"></span>
    {/if}
  </button>

  <!-- Provider selector -->
  {#if providers.length > 0}
    <div class="relative">
      <button
        type="button"
        aria-label="Select provider"
        onclick={toggleProviderMenu}
        class="inline-flex items-center gap-1.5 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 px-2.5 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600 transition-colors"
      >
        <svg viewBox="0 0 12 12" class="h-3 w-3 text-green-500" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <circle cx="6" cy="4.5" r="2"></circle>
          <path d="M2 10c0-2.21 1.79-4 4-4s4 1.79 4 4" stroke-linecap="round"></path>
        </svg>
        <span>{activeProvider ?? ""}</span>
        <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M3 4.5l3 3 3-3" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </button>
      {#if providerMenuOpen}
        <div class="absolute bottom-full left-0 mb-1 z-20 min-w-[140px] rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg py-1">
          {#each providers as p}
            <button
              type="button"
              aria-label={p.type}
              onclick={() => selectProvider(p.type)}
              class="w-full text-left px-3 py-2 text-xs text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors flex items-center justify-between gap-2"
            >
              {p.type}
              {#if p.name && p.name !== p.type}
                <span class="text-black-600 dark:text-black-700">{p.name}</span>
              {/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- Project selector -->
  {#if projects.length > 0}
    <div class="relative">
      <button
        type="button"
        aria-label="Select project"
        onclick={toggleProjectMenu}
        class="inline-flex items-center gap-1.5 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 px-2.5 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600 transition-colors"
      >
        <svg viewBox="0 0 12 12" class="h-3 w-3 text-black-600 dark:text-black-700" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <rect x="1" y="3" width="10" height="8" rx="1"></rect>
          <path d="M4 3V2.5a.5.5 0 01.5-.5h3a.5.5 0 01.5.5V3" stroke-linecap="round"></path>
        </svg>
        <span>{activeProjectName}</span>
        <svg viewBox="0 0 12 12" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M3 4.5l3 3 3-3" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </button>
      {#if projectMenuOpen}
        <div class="absolute bottom-full left-0 mb-1 z-20 min-w-[160px] rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg py-1">
          <button
            type="button"
            aria-label="— no project —"
            onclick={() => selectProject(null)}
            class="w-full text-left px-3 py-2 text-xs text-black-700 dark:text-black-600 italic hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
          >— no project —</button>
          {#each projects as p}
            <button
              type="button"
              aria-label={p.name}
              onclick={() => selectProject(p.id)}
              class="w-full text-left px-3 py-2 text-xs text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 transition-colors"
            >{p.name}</button>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</div>
