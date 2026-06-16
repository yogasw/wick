<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import { ToastHost } from "@wick-fe/common-ui";
  import PresetsList from "$lib/components/PresetsList.svelte";
  import PresetEditor from "$lib/components/PresetEditor.svelte";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let editorParams = $derived(match("/presets/:name", currentRoute));
</script>

<div class="min-h-screen p-6">
  <ToastHost />
  {#if editorParams}
    <PresetEditor name={editorParams.name} onBack={() => push("/")} />
  {:else}
    <PresetsList onNavigate={(name) => push(`/presets/${encodeURIComponent(name)}`)} />
  {/if}
</div>
