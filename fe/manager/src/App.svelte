<script lang="ts">
  import { get } from "svelte/store";
  import { route } from "$lib/router.js";
  import AppShell from "$lib/components/AppShell.svelte";
  import ConnectorsIndex from "$lib/components/ConnectorsIndex.svelte";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });
</script>

<AppShell>
  {#snippet breadcrumb()}
    <span>Connectors</span>
  {/snippet}
  {#key currentRoute}
    <ConnectorsIndex />
  {/key}
</AppShell>
