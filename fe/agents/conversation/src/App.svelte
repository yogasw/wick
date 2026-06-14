<script lang="ts">
  import { ToastHost } from "@wick-fe/common-ui";
  import { route, match, push, initialRoute } from "./lib/router.js";
  import ListView from "./lib/components/ListView.svelte";
  import DetailView from "./lib/components/DetailView.svelte";

  const appEl = document.getElementById("app");
  const base = appEl?.dataset.base ?? "";

  const deepLink = initialRoute(window.location.hash.startsWith("#") ? window.location.hash.slice(1) : window.location.hash, appEl?.dataset.initialSession);
  if (deepLink) push(deepLink);

  let currentRoute = $state("/");
  route.subscribe((v) => { currentRoute = v; });

  const detailParams = $derived(match("/sessions/:id", currentRoute));
</script>

<ToastHost />

{#if detailParams}
  <DetailView {base} sessionId={detailParams.id} />
{:else}
  <ListView {base} />
{/if}
