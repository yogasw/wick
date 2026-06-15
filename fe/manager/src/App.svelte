<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import AppShell from "$lib/components/AppShell.svelte";
  import ConnectorsIndex from "$lib/components/ConnectorsIndex.svelte";
  import ConnectorList from "$lib/components/ConnectorList.svelte";
  import ConnectorDetail from "$lib/components/ConnectorDetail.svelte";
  import ConnectorTest from "$lib/components/ConnectorTest.svelte";
  import ConnectorHistory from "$lib/components/ConnectorHistory.svelte";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let testParams = $derived(match("/connectors/:key/:id/test", currentRoute));
  let historyParams = $derived(match("/connectors/:key/:id/history", currentRoute));
  let detailParams = $derived(match("/connectors/:key/:id", currentRoute));
  let listParams = $derived(match("/connectors/:key", currentRoute));

  let rowCrumb = $derived(testParams ?? historyParams ?? detailParams);
</script>

<AppShell>
  {#snippet breadcrumb()}
    <button type="button" class="hover:text-green-600" onclick={() => push("/")}>Connectors</button>
    {#if listParams}
      <span aria-hidden="true"> / </span>
      <span class="text-black-900 dark:text-white-100">{listParams.key}</span>
    {:else if rowCrumb}
      <span aria-hidden="true"> / </span>
      <button type="button" class="hover:text-green-600" onclick={() => push(`/connectors/${encodeURIComponent(rowCrumb.key)}`)}>{rowCrumb.key}</button>
      <span aria-hidden="true"> / </span>
      {#if testParams || historyParams}
        <button type="button" class="hover:text-green-600" onclick={() => push(`/connectors/${encodeURIComponent(rowCrumb.key)}/${encodeURIComponent(rowCrumb.id)}`)}>{rowCrumb.id}</button>
        <span aria-hidden="true"> / </span>
        <span class="text-black-900 dark:text-white-100">{testParams ? "Test" : "History"}</span>
      {:else}
        <span class="text-black-900 dark:text-white-100">{rowCrumb.id}</span>
      {/if}
    {/if}
  {/snippet}
  {#key currentRoute}
    {#if testParams}
      <ConnectorTest connectorKey={testParams.key} connectorId={testParams.id} />
    {:else if historyParams}
      <ConnectorHistory connectorKey={historyParams.key} connectorId={historyParams.id} />
    {:else if detailParams}
      <ConnectorDetail connectorKey={detailParams.key} connectorId={detailParams.id} />
    {:else if listParams}
      <ConnectorList connectorKey={listParams.key} />
    {:else}
      <ConnectorsIndex />
    {/if}
  {/key}
</AppShell>
