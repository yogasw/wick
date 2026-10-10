<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";
  import TeamIcon from "./TeamIcon.svelte";
  import type { TeamIconName } from "./icons.js";

  /* The small button of the Team blocks (mockup .btn.sm): "Reply in
     thread", "Open chat", "Show all". warn is the amber one that answers a
     question ("Answer"), danger the red text one ("Cancel"). An icon, when
     given, sits before the label at 12px. rounded is wick's 8px, the
     mockup's radius (rounded-lg is 12px here). */
  type Props = HTMLButtonAttributes & {
    variant?: "default" | "warn" | "danger";
    icon?: TeamIconName;
    children: Snippet;
  };
  let { variant = "default", icon, children, class: cls = "", type = "button", ...rest }: Props = $props();

  const tone: Record<NonNullable<Props["variant"]>, string> = {
    default: "border-white-300 bg-white-100 text-black-900 hover:bg-white-200 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100 dark:hover:bg-navy-700",
    // The mockup's warn tokens (§0); the wick palette has no amber-400.
    warn: "border-amber-500/[.45] bg-[rgba(251,191,36,.12)] text-amber-700 dark:border-[rgba(251,191,36,.40)] dark:bg-[rgba(251,191,36,.10)] dark:text-amber-300",
    danger: "border-white-300 bg-white-100 text-red-500 hover:bg-white-200 dark:border-navy-600 dark:bg-navy-800 dark:hover:bg-navy-700",
  };
</script>

<button
  {type}
  class="inline-flex items-center gap-1.5 rounded border px-2 py-[3px] text-[10.5px] font-medium leading-normal disabled:cursor-not-allowed disabled:opacity-50 {tone[variant]} {cls}"
  data-variant={variant}
  {...rest}
>
  {#if icon}<TeamIcon name={icon} />{/if}
  {@render children()}
</button>
