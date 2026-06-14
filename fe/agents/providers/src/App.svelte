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
  {#if !isStorage && !detailParams}
    <div class="mb-5 flex items-center gap-2">
      <button
        onclick={() => push("/")}
        class={[
          "rounded-lg px-4 py-2 text-xs font-medium border transition-colors",
          currentRoute === "/"
            ? "bg-green-500 border-green-500 text-white-100"
            : "border-white-400 dark:border-navy-600 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800",
        ].join(" ")}
      >Providers</button>
      <button
        onclick={() => push("/storage")}
        class="rounded-lg px-4 py-2 text-xs font-medium border border-white-400 dark:border-navy-600 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
      >Storage</button>
    </div>
  {/if}
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
