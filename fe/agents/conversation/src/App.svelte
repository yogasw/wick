<script lang="ts">
  import { ToastHost } from "@wick-fe/common-ui";
  import { route, match } from "./lib/router.js";
  import ListView from "./lib/components/ListView.svelte";
  import DetailView from "./lib/components/DetailView.svelte";

  const appEl = document.getElementById("app");
  const base = appEl?.dataset.base ?? "";

  let currentRoute = $state("/");
  route.subscribe((v) => { currentRoute = v; });

  const detailParams = $derived(match("/sessions/:id", currentRoute));
</script>

<ToastHost />

{#if detailParams}
  <!-- key on the session id so navigating between sessions remounts DetailView
       (fresh onMount → SSE reconnects + history reloads for the new session). -->
  {#key detailParams.id}
    <DetailView {base} sessionId={detailParams.id} />
  {/key}
{:else}
  <ListView {base} />
{/if}
