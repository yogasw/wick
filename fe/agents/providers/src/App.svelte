<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import { ToastHost } from "@wick-fe/common-ui";
  import ProvidersList from "$lib/components/ProvidersList.svelte";

  const base = document.getElementById("app")?.dataset.base ?? "";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let detailParams = $derived(match("/:type/:name", currentRoute));
</script>

<div class="min-h-screen p-6">
  <ToastHost />
  {#if detailParams}
    <!-- detail view — next slice; navigate back to list for now -->
    <div class="space-y-4">
      <button onclick={() => push("/")} class="text-xs text-black-600 dark:text-black-500 hover:underline">← Back to Providers</button>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">
        Provider detail view for <strong>{detailParams.type}/{detailParams.name}</strong> — coming in the next slice.
      </div>
    </div>
  {:else}
    <ProvidersList
      {base}
      onNavigate={(type, name) => push(`/${encodeURIComponent(type)}/${encodeURIComponent(name)}`)}
    />
  {/if}
</div>
