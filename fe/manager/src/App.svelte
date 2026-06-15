<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import AppShell from "$lib/components/AppShell.svelte";
  import ConnectorsIndex from "$lib/components/ConnectorsIndex.svelte";
  import ConnectorList from "$lib/components/ConnectorList.svelte";
  import ConnectorDetail from "$lib/components/ConnectorDetail.svelte";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let detailParams = $derived(match("/connectors/:key/:id", currentRoute));
  let listParams = $derived(match("/connectors/:key", currentRoute));
</script>

<AppShell>
  {#snippet breadcrumb()}
    <button type="button" class="hover:text-green-600" onclick={() => push("/")}>Connectors</button>
    {#if listParams}
      <span aria-hidden="true"> / </span>
      <span class="text-black-900 dark:text-white-100">{listParams.key}</span>
    {:else if detailParams}
      <span aria-hidden="true"> / </span>
      <button type="button" class="hover:text-green-600" onclick={() => push(`/connectors/${encodeURIComponent(detailParams.key)}`)}>{detailParams.key}</button>
      <span aria-hidden="true"> / </span>
      <span class="text-black-900 dark:text-white-100">{detailParams.id}</span>
    {/if}
  {/snippet}
  {#key currentRoute}
    {#if detailParams}
      <ConnectorDetail connectorKey={detailParams.key} connectorId={detailParams.id} />
    {:else if listParams}
      <ConnectorList connectorKey={listParams.key} />
    {:else}
      <ConnectorsIndex />
    {/if}
  {/key}
</AppShell>
