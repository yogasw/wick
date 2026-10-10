<script lang="ts">
  import { TEAM_ICONS, type TeamIconName } from "./icons.js";

  /* One of the small Team icons (icons.ts), in currentColor. Decorative:
     the text next to it names the action. spin turns it (the working
     arc), still under reduced motion. */
  let { name, size = 12, spin = false, class: cls = "" }: { name: TeamIconName; size?: number; spin?: boolean; class?: string } = $props();
</script>

<svg
  class="shrink-0 {cls}"
  class:team-icon-spin={spin}
  width={size}
  height={size}
  viewBox="0 0 16 16"
  aria-hidden="true"
  data-icon={name}
>{@html TEAM_ICONS[name]}</svg>

<style>
  .team-icon-spin { animation: team-icon-spin 1s linear infinite; }
  @keyframes team-icon-spin {
    to { transform: rotate(360deg); }
  }
  @media (prefers-reduced-motion: reduce) {
    .team-icon-spin { animation: none; }
  }
</style>
