<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import { ToastHost } from "@wick-fe/common-ui";
  import ProvidersList from "$lib/components/ProvidersList.svelte";
  import ProviderDetail from "$lib/components/ProviderDetail.svelte";
  import StorageView from "$lib/components/StorageView.svelte";
  import SpawnDetail from "$lib/components/SpawnDetail.svelte";

  const base = document.getElementById("app")?.dataset.base ?? "";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let isStorage = $derived(currentRoute === "/storage");
  // "/spawns/<file>" and "/:type/:name" are both 2-segment — match spawn first.
  let spawnParams = $derived(!isStorage ? match("/spawns/:file", currentRoute) : null);
  let detailParams = $derived(!isStorage && !spawnParams ? match("/:type/:name", currentRoute) : null);
</script>

<div class="min-h-screen p-6">
  <ToastHost />
  {#if isStorage}
    <StorageView onBack={() => push("/")} />
  {:else if spawnParams}
    <SpawnDetail {base} file={spawnParams.file} onBack={() => push("/")} />
  {:else if detailParams}
    <ProviderDetail
      {base}
      type={detailParams.type}
      name={detailParams.name}
      onBack={() => push("/")}
      onOpenSpawn={(f) => push(`/spawns/${encodeURIComponent(f)}`)}
    />
  {:else}
    <ProvidersList
      {base}
      onNavigate={(type, name) => push(`/${encodeURIComponent(type)}/${encodeURIComponent(name)}`)}
      onOpenSpawn={(f) => push(`/spawns/${encodeURIComponent(f)}`)}
    />
  {/if}
</div>
