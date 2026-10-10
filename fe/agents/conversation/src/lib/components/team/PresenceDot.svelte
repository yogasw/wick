<script lang="ts">
  import type { Presence } from "../../rosterStatus.js";

  /* The status dot on an agent's avatar (roster row and chat header): amber
     when it needs the owner, grey when disabled. Online and working draw no
     dot (the avatar itself acts working out), as the mockup. No tooltip: label is read out instead (sr-only), so the
     row or header still says it to a screen reader. mark replaces the dot
     on a Team task: "asks" = the amber "?" of a question back to its
     sender, "task" = green, working on a task another agent sent. */
  let {
    presence,
    label,
    ring = "border-white-100 dark:border-navy-700",
    mark = null,
  }: { presence: Presence; label: string; ring?: string; mark?: "asks" | "task" | null } = $props();

  const fill: Record<"attention" | "disabled", string> = {
    attention: "bg-amber-500",
    disabled: "bg-black-600",
  };
</script>

{#if mark === "asks"}
  <span class="pointer-events-none absolute -bottom-[3px] -right-[3px] grid h-[15px] w-[15px] place-items-center rounded-full border-2 bg-amber-500 text-[10px] font-extrabold leading-none text-[#1b1300] {ring}" data-testid="presence-dot" data-presence={presence} data-mark="asks" aria-hidden="true">?</span>
{:else if mark === "task"}
  <span class="pointer-events-none absolute -bottom-1 -right-1 grid h-[15px] w-[15px] place-items-center rounded-full border-2 bg-green-500 text-white-100 {ring}" data-testid="presence-dot" data-presence={presence} data-mark="task" aria-hidden="true"
    ><svg class="h-2 w-2" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="5" cy="5" r="2" /><path d="M2 12a3 3 0 016 0" /><path d="M10 4.5h4M10 8h4M10 11.5h2.5" /></svg></span
  >
{:else if presence === "attention" || presence === "disabled"}
  <span class="pointer-events-none absolute bottom-0 right-0 h-3 w-3 rounded-full border-2 {ring} {fill[presence]}" data-testid="presence-dot" data-presence={presence} aria-hidden="true"></span>
{/if}
<span class="sr-only" data-testid="presence-label">{label}</span>
