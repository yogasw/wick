<script lang="ts">
  import type { Snippet } from "svelte";
  import { ToastHost } from "@wick-fe/common-ui";

  type Props = {
    breadcrumb?: Snippet;
    children: Snippet;
  };
  let { breadcrumb, children }: Props = $props();
</script>

<ToastHost />

<div class="flex flex-col gap-4">
  {#if breadcrumb}
    <nav
      class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm text-black-700 dark:text-black-600"
      aria-label="Breadcrumb"
    >
      {@render breadcrumb()}
    </nav>
  {/if}

  <main>
    {@render children()}
  </main>
</div>
