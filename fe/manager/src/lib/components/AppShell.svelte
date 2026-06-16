<script lang="ts">
  import type { Snippet } from "svelte";
  import { ToastHost, Breadcrumb } from "@wick-fe/common-ui";
  import type { BreadcrumbItem } from "@wick-fe/common-ui";

  type Props = {
    items?: BreadcrumbItem[];
    children: Snippet;
  };
  let { items, children }: Props = $props();
</script>

<ToastHost />

<div class="flex flex-col gap-4">
  {#if items && items.length > 0}
    <Breadcrumb {items} />
  {/if}

  <main>
    {@render children()}
  </main>
</div>
