<script lang="ts">
  /* A live card an agent wrote as an `actioncard` fence. Clicking a button
     posts it back to the server, which records the click as a user turn the
     agent receives — it is a reply, not a permission: anything risky still
     goes through the gate or ask_user. A newer version of the same id
     elsewhere in the thread replaces this one, which then collapses. */
  import type { ActionCardData, CardMode } from "../../actionCard.js";
  import type { CardPostback } from "../../types/agents.js";

  type Props = {
    card: ActionCardData;
    mode: CardMode;
    /** The click that locked the card, when there was one. */
    clicked?: CardPostback;
    onAction?: (cardId: string, value: string, label: string) => void;
  };
  let { card, mode, clicked, onAction }: Props = $props();
  let sending = $state("");

  const ICONS: Record<string, string> = {
    shield: "M8 1.5 13 3.5v4c0 3-2.2 5.4-5 6.5-2.8-1.1-5-3.5-5-6.5v-4z",
    check: "M3 8.5 6.5 12 13 4.5",
    alert: "M8 2 1.5 13.5h13zM8 6.5v3M8 11.5v.1",
    user: "M8 7.5a2.5 2.5 0 100-5 2.5 2.5 0 000 5zM3 14c.6-2.6 2.6-4 5-4s4.4 1.4 5 4",
    clock: "M8 14A6 6 0 108 2a6 6 0 000 12zM8 4.5V8l2.5 1.5",
    arrow: "M2.5 8h10M9 4.5 12.5 8 9 11.5",
  };
  const iconPath = $derived(ICONS[card.icon ?? ""] ?? "M3 3.5h10v9H3zM5.5 6.5h5M5.5 9h3");

  function click(value: string, label: string) {
    if (mode !== "active" || sending) return;
    sending = value;
    try { onAction?.(card.id, value, label); } finally { setTimeout(() => { sending = ""; }, 1500); }
  }
</script>

{#if mode === "superseded"}
  <div data-testid="actioncard" data-mode="superseded" class="my-1.5 flex items-center gap-1.5 rounded-lg border border-dashed border-white-300 dark:border-navy-600 px-3 py-1 text-[11px] text-black-600 dark:text-black-500 not-prose">
    <span class="truncate line-through opacity-70">{card.title || card.id}</span>
    <span class="shrink-0 opacity-70">· superseded</span>
  </div>
{:else}
  <div data-testid="actioncard" data-mode={mode} class="not-prose my-2 flex w-full max-w-md flex-col gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3.5 py-3 text-sm shadow-sm">
    <div class="flex items-start gap-2.5">
      <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-green-500/10 text-green-600 dark:text-green-400">
        <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={iconPath}></path></svg>
      </span>
      <div class="flex min-w-0 flex-1 flex-col">
        <span class="font-semibold text-black-900 dark:text-white-100 break-words">{card.title || card.id}</span>
        {#if card.subtitle}<span class="text-xs text-black-600 dark:text-black-500 break-words">{card.subtitle}</span>{/if}
      </div>
      {#if card.status}
        <span data-testid="actioncard-status" class="shrink-0 rounded-full bg-white-200 dark:bg-navy-700 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">{card.status}</span>
      {/if}
    </div>
    {#if card.rows && card.rows.length > 0}
      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
        {#each card.rows as [k, val]}
          <dt class="text-black-600 dark:text-black-500">{k}</dt>
          <dd class="min-w-0 break-words text-black-900 dark:text-white-100">{val}</dd>
        {/each}
      </dl>
    {/if}
    {#if card.actions && card.actions.length > 0}
      <div class="flex flex-wrap items-center gap-1.5 pt-0.5">
        {#each card.actions as a}
          <button
            type="button"
            data-testid="actioncard-btn"
            disabled={mode !== "active" || !!sending}
            onclick={() => click(a.value, a.label)}
            class={"rounded-lg px-3 py-1 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 " +
              (a.style === "primary"
                ? "bg-green-500 text-white-100 hover:bg-green-600"
                : "border border-white-300 dark:border-navy-600 text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700") +
              (clicked?.value === a.value ? " ring-2 ring-green-500/50" : "")}
          >{a.label}</button>
        {/each}
        {#if mode === "locked" && clicked}
          <span class="text-[11px] text-black-600 dark:text-black-500">✓ {clicked.label}</span>
        {/if}
      </div>
    {/if}
  </div>
{/if}
