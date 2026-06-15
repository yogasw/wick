<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import { ToastHost } from "@wick-fe/common-ui";
  import ProvidersList from "$lib/components/ProvidersList.svelte";
  import ProviderDetail from "$lib/components/ProviderDetail.svelte";
  import StorageView from "$lib/components/StorageView.svelte";

  const base = document.getElementById("app")?.dataset.base ?? "";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let isStorage = $derived(currentRoute === "/storage");
  let detailParams = $derived(!isStorage ? match("/:type/:name", currentRoute) : null);
</script>

<div class="min-h-screen p-6">
  <ToastHost />
  {#if isStorage}
    <StorageView onBack={() => push("/")} />
  {:else if detailParams}
    <ProviderDetail
      {base}
      type={detailParams.type}
      name={detailParams.name}
      onBack={() => push("/")}
    />
  {:else}
    <ProvidersList
      {base}
      onNavigate={(type, name) => push(`/${encodeURIComponent(type)}/${encodeURIComponent(name)}`)}
    />
  {/if}
</div>
