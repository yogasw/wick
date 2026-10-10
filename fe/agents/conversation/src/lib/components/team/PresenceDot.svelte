<script lang="ts">
  import type { Presence } from "../../rosterStatus.js";

  /* The status dot on an agent's avatar (roster row and chat header): green
     online, a spinner while working, amber when it needs the owner, grey
     when disabled. No tooltip: label is read out instead (sr-only), so the
     row or header still says it to a screen reader. */
  let { presence, label, ring = "border-white-100 dark:border-navy-700" }: { presence: Presence; label: string; ring?: string } = $props();

  const fill: Record<Presence, string> = {
    online: "bg-green-500",
    working: "",
    attention: "bg-amber-500",
    disabled: "bg-black-600",
  };
</script>

<span class="pointer-events-none absolute bottom-0 right-0 h-3 w-3 rounded-full border-2 {ring} {presence === 'working' ? 'presence-spin bg-white-100 dark:bg-navy-700' : fill[presence]}" data-testid="presence-dot" data-presence={presence} aria-hidden="true"></span>
<span class="sr-only" data-testid="presence-label">{label}</span>

<style>
  /* A green ring with a gap that turns: the "working" dot. */
  .presence-spin {
    box-shadow: inset 0 0 0 2px #22c55e;
    border-top-color: transparent;
    animation: presence-spin 1s linear infinite;
  }
  @keyframes presence-spin {
    to { transform: rotate(360deg); }
  }
  @media (prefers-reduced-motion: reduce) {
    .presence-spin { animation: none; }
  }
</style>
