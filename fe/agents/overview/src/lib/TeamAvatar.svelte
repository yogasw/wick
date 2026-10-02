<script lang="ts">
  /* A still copy of the Team app's blob avatar for the Overview card: same
     four shapes and colour, no animation (the living one, with its shared
     rAF loop, belongs to the conversation bundle). */
  type Props = { shape?: string; color?: string; size?: number; asleep?: boolean };
  let { shape = "circle", color = "", size = 36, asleep = false }: Props = $props();

  const fill = $derived(/^#[0-9a-fA-F]{3,8}$/.test(color) ? color : "#6366f1");
</script>

<svg width={size} height={size} viewBox="0 0 40 40" class="shrink-0 {asleep ? 'opacity-50' : ''}" aria-hidden="true">
  {#if shape === "squircle"}
    <rect x="2" y="2" width="36" height="36" rx="12" fill={fill}></rect>
  {:else if shape === "triangle"}
    <path d="M20 3 L37.5 34 Q38.5 37 35.5 37 H4.5 Q1.5 37 2.5 34 Z" fill={fill} stroke={fill} stroke-width="2" stroke-linejoin="round"></path>
  {:else if shape === "diamond"}
    <path d="M20 2 L38 20 L20 38 L2 20 Z" fill={fill} stroke={fill} stroke-width="3" stroke-linejoin="round"></path>
  {:else}
    <circle cx="20" cy="20" r="18" fill={fill}></circle>
  {/if}
  {#if asleep}
    <path d="M13 23 h5 M22 23 h5" stroke="#0f172a" stroke-opacity="0.8" stroke-width="2" stroke-linecap="round"></path>
  {:else}
    <circle cx="15.5" cy="22" r="2.6" fill="#0f172a" fill-opacity="0.85"></circle>
    <circle cx="24.5" cy="22" r="2.6" fill="#0f172a" fill-opacity="0.85"></circle>
  {/if}
</svg>
