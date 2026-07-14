<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import { ToastHost } from "@wick-fe/common-ui";
  import ProvidersList from "$lib/components/ProvidersList.svelte";
  import ProviderDetail from "$lib/components/ProviderDetail.svelte";
  import StorageView from "$lib/components/StorageView.svelte";
  import SessionDetail from "$lib/components/SessionDetail.svelte";
  import LogViewer from "$lib/components/LogViewer.svelte";

  const base = document.getElementById("app")?.dataset.base ?? "";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let isStorage = $derived(currentRoute === "/storage");
  let isLogs = $derived(currentRoute === "/logs");
  // "/session/<id>" and "/:type/:name" are both 2-segment — match session first.
  let sessionParams = $derived(!isStorage && !isLogs ? match("/session/:id", currentRoute) : null);
  let detailParams = $derived(!isStorage && !isLogs && !sessionParams ? match("/:type/:name", currentRoute) : null);

  // Log viewer reads ?file= (+ optional ?from=&to= spawn window) from the URL.
  let logParams = $derived.by(() => {
    if (!isLogs) return null;
    const q = new URLSearchParams(window.location.search);
    const file = q.get("file");
    if (!file) return null;
    return { file, from: q.get("from") ?? undefined, to: q.get("to") ?? undefined };
  });

  function openLog(file: string, from?: string, to?: string): void {
    const q = new URLSearchParams({ file });
    if (from) q.set("from", from);
    if (to) q.set("to", to);
    push(`/logs?${q.toString()}`);
  }
</script>

<div class="min-h-screen p-6">
  <ToastHost />
  {#if isStorage}
    <StorageView onBack={() => push("/")} />
  {:else if logParams}
    <LogViewer {base} file={logParams.file} from={logParams.from} to={logParams.to} onBack={() => history.back()} />
  {:else if sessionParams}
    <SessionDetail {base} id={sessionParams.id} onBack={() => push("/")} onOpenLog={openLog} />
  {:else if detailParams}
    <ProviderDetail
      {base}
      type={detailParams.type}
      name={detailParams.name}
      onBack={() => push("/")}
      onOpenSession={(id) => push(`/session/${encodeURIComponent(id)}`)}
    />
  {:else}
    <ProvidersList
      {base}
      onNavigate={(type, name) => push(`/${encodeURIComponent(type)}/${encodeURIComponent(name)}`)}
      onOpenSession={(id) => push(`/session/${encodeURIComponent(id)}`)}
    />
  {/if}
</div>
