<script lang="ts">
  /* Field wrapper: label (+ required asterisk) over any control, with helper
     or error text below. The control is supplied via the children snippet so
     this composes with TextInput / NumberInput / Select / KvList / etc. */
  import type { Snippet } from "svelte";

  type Props = {
    label?: string;
    helper?: string;
    error?: string;
    required?: boolean;
    children: Snippet;
  };

  let { label, helper, error, required = false, children }: Props = $props();
</script>

<div class="flex flex-col gap-1">
  {#if label}
    <span class="flex items-center gap-0.5 text-xs font-medium text-black-800 dark:text-white-100">
      <span>{label}</span>
      {#if required}<span class="text-rose-500">*</span>{/if}
    </span>
  {/if}
  {@render children()}
  {#if error}
    <span class="text-[11px] text-rose-600 dark:text-rose-400">{error}</span>
  {:else if helper}
    <span class="text-[11px] text-black-700 dark:text-black-600">{helper}</span>
  {/if}
</div>
