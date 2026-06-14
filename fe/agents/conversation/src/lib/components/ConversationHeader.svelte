<script lang="ts">
  import type { SSEStatus } from "../types/agents.js";
  import type { LifecycleState } from "../stores/thread.js";
  import { idleCountdownText } from "../idleCountdown.js";

  export type ActiveView = "conversation" | "commands" | "approvals" | "raw";

  const TAB_LABELS: Record<ActiveView, string> = {
    conversation: "Conversation",
    commands: "Commands",
    approvals: "Approvals",
    raw: "Raw",
  };

  const TAB_ORDER: ActiveView[] = ["conversation", "commands", "approvals", "raw"];

  type Props = {
    title: string;
    agentLabel?: string;
    sseStatus: SSEStatus;
    lifecycle?: LifecycleState;
    idleTimeoutMs?: number;
    activeView?: ActiveView;
    onKill: () => void;
    onDelete: () => void;
    onTabChange?: (view: ActiveView) => void;
  };

  let {
    title,
    agentLabel = "",
    sseStatus,
    lifecycle,
    idleTimeoutMs = 120_000,
    activeView = "conversation",
    onKill,
    onDelete,
    onTabChange,
  }: Props = $props();

  let tabMenuOpen = $state(false);

  function toggleTabMenu() {
    tabMenuOpen = !tabMenuOpen;
  }

  function selectTab(view: ActiveView) {
    tabMenuOpen = false;
    onTabChange?.(view);
  }

  const statusClass = $derived(
    sseStatus === "connected"
      ? "border-green-300 dark:border-green-700 text-green-700 dark:text-green-300"
      : sseStatus === "error"
        ? "border-neg-300 dark:border-neg-700 text-neg-600 dark:text-neg-400"
        : "border-white-300 dark:border-navy-600 text-black-600 dark:text-black-700",
  );

  const statusLabel = $derived(
    sseStatus === "connected" ? "live" : sseStatus === "error" ? "error" : "connecting",
  );

  const lcVisible = $derived(lifecycle && lifecycle.state !== "" && lifecycle.state !== "killed");

  const lcClass = $derived(
    lifecycle?.state === "spawning"
      ? "border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-300"
      : lifecycle?.state === "working"
        ? "border-green-300 dark:border-green-700 bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-300"
        : "border-blue-300 dark:border-blue-700 bg-blue-50 dark:bg-blue-900/20 text-blue-700 dark:text-blue-300",
  );

  const lcLabel = $derived(
    lifecycle?.state === "spawning"
      ? (lifecycle.substate ? lifecycle.substate : "spawning…")
      : lifecycle?.state === "working"
        ? (lifecycle.substate ? lifecycle.substate : "working")
        : lifecycle?.state === "idle"
          ? "idle"
          : "",
  );

  /* idle countdown state */
  let idleEnteredAt = $state(0);
  let countdownText = $state("");

  $effect(() => {
    if (lifecycle?.state !== "idle") {
      countdownText = "";
      idleEnteredAt = 0;
      return;
    }

    const atMs = lifecycle.at || (idleEnteredAt || Date.now());
    if (!idleEnteredAt) idleEnteredAt = atMs;

    countdownText = idleCountdownText(atMs, idleTimeoutMs, Date.now());

    const timer = setInterval(() => {
      countdownText = idleCountdownText(atMs, idleTimeoutMs, Date.now());
    }, 1000);

    return () => clearInterval(timer);
  });
</script>

<div
  class="relative z-30 shrink-0 flex flex-wrap items-center justify-between gap-2 pl-2 pr-2 md:px-4 py-2 bg-white-100/80 dark:bg-navy-800/80 backdrop-blur-sm border-b border-white-300 dark:border-navy-600"
>
  <!-- Left: tab dropdown burger + title + agent label -->
  <div class="flex items-center gap-2 min-w-0 flex-1">
    <!-- Tab dropdown -->
    <div class="relative shrink-0">
      <button
        type="button"
        aria-label="Tab menu"
        onclick={toggleTabMenu}
        class="inline-flex items-center gap-1.5 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 px-2.5 py-1.5 text-xs font-medium text-black-800 dark:text-black-300 hover:bg-white-300 dark:hover:bg-navy-600 transition-colors"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M2 4h12M2 8h12M2 12h12" stroke-linecap="round"></path>
        </svg>
        <span>{TAB_LABELS[activeView]}</span>
        <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M3 4.5l3 3 3-3" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </button>
      {#if tabMenuOpen}
        <div
          data-tab-dropdown
          class="absolute top-full left-0 mt-1 z-50 min-w-[160px] rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg py-1"
        >
          {#each TAB_ORDER as tab}
            <button
              type="button"
              aria-label={TAB_LABELS[tab]}
              onclick={() => selectTab(tab)}
              class={[
                "w-full text-left px-3 py-2 text-xs transition-colors flex items-center justify-between gap-2",
                activeView === tab
                  ? "text-green-700 dark:text-green-400 font-medium bg-green-50 dark:bg-green-900/20"
                  : "text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700",
              ].join(" ")}
            >
              {TAB_LABELS[tab]}
              {#if activeView === tab}
                <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0 text-green-500" fill="currentColor" aria-hidden="true">
                  <path d="M2 6.5L5 9.5L10 3" stroke="currentColor" stroke-width="1.5" fill="none" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              {/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <span class="font-semibold text-sm text-black-900 dark:text-white-100 truncate">{title}</span>
    {#if agentLabel}
      <span class="hidden md:inline text-xs text-black-600 dark:text-black-700 shrink-0"
        >{agentLabel}</span
      >
    {/if}
  </div>

  <!-- Right: lifecycle + SSE status + Kill + Delete -->
  <div class="flex items-center gap-2 shrink-0">
    {#if lcVisible}
      <span
        data-lifecycle-badge
        class={[
          "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium",
          lcClass,
        ].join(" ")}
      >
        {#if lifecycle?.state === "spawning" || lifecycle?.state === "working"}
          <svg viewBox="0 0 8 8" class="h-1.5 w-1.5 animate-pulse" fill="currentColor">
            <circle cx="4" cy="4" r="3"></circle>
          </svg>
        {:else}
          <svg viewBox="0 0 8 8" class="h-1.5 w-1.5" fill="currentColor">
            <circle cx="4" cy="4" r="3"></circle>
          </svg>
        {/if}
        <span data-lifecycle-label>{lcLabel}</span>
        {#if lifecycle?.state === "idle" && countdownText}
          <span data-idle-countdown class="ml-0.5 opacity-80">{countdownText}</span>
        {/if}
      </span>
    {/if}
    <span
      class={[
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium",
        statusClass,
      ].join(" ")}
    >
      {#if sseStatus === "connecting"}
        <svg
          viewBox="0 0 16 16"
          class="h-2.5 w-2.5 animate-spin"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
        >
          <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
        </svg>
      {:else}
        <svg viewBox="0 0 8 8" class="h-1.5 w-1.5" fill="currentColor">
          <circle cx="4" cy="4" r="3"></circle>
        </svg>
      {/if}
      {statusLabel}
    </span>

    <button
      type="button"
      aria-label="Kill session"
      onclick={onKill}
      class="inline-flex items-center gap-1.5 rounded-lg border border-cau-400 dark:border-cau-600 px-2 md:px-3 py-1.5 text-xs font-medium text-cau-600 dark:text-cau-400 hover:bg-cau-50 dark:hover:bg-cau-900/20 transition-colors"
    >
      <svg
        viewBox="0 0 12 12"
        class="h-3 w-3"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
      >
        <circle cx="6" cy="6" r="4.5"></circle>
        <path d="M4 4l4 4M8 4L4 8" stroke-linecap="round"></path>
      </svg>
      <span class="hidden md:inline">Kill</span>
    </button>

    <button
      type="button"
      aria-label="Delete session"
      onclick={onDelete}
      class="inline-flex items-center gap-1.5 rounded-lg border border-neg-300 dark:border-neg-700 px-2 md:px-3 py-1.5 text-xs font-medium text-neg-600 dark:text-neg-400 hover:bg-neg-50 dark:hover:bg-neg-900/20 transition-colors"
    >
      <svg
        viewBox="0 0 12 12"
        class="h-3 w-3"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
      >
        <path
          d="M2 3h8M4 3V2h4v1M5 5.5v3M7 5.5v3M3 3l.5 7h5L9 3"
          stroke-linecap="round"
          stroke-linejoin="round"
        ></path>
      </svg>
      <span class="hidden md:inline">Delete</span>
    </button>
  </div>
</div>
