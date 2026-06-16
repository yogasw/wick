<script lang="ts">
  import { get } from "svelte/store";
  import { route, match, push } from "$lib/router.js";
  import { ToastHost } from "@wick-fe/common-ui";
  import { buildSkillFileCrumbs } from "$lib/skillCrumbs.js";
  import SkillsList from "$lib/components/SkillsList.svelte";
  import SkillDetail from "$lib/components/SkillDetail.svelte";
  import SkillFileView from "$lib/components/SkillFileView.svelte";

  let currentRoute = $state(get(route));
  $effect(() => {
    const unsub = route.subscribe((r) => { currentRoute = r; });
    return unsub;
  });

  let fileParams = $derived(match("/skills/:folder/files/:file...", currentRoute));
  let detailParams = $derived(match("/skills/:name", currentRoute));
</script>

<div class="min-h-screen p-6">
  <ToastHost />
  {#if fileParams}
    <SkillFileView
      folder={fileParams.folder}
      file={fileParams.file}
      breadcrumb={buildSkillFileCrumbs(fileParams.folder, fileParams.file, push)}
      onOpenChild={(childPath) => push(`/skills/${encodeURIComponent(fileParams!.folder)}/files/${childPath.split("/").map(encodeURIComponent).join("/")}`)}
    />
  {:else if detailParams}
    <SkillDetail
      name={detailParams.name}
      breadcrumb={[
        { label: "Skills", onClick: () => push("/") },
        { label: detailParams.name },
      ]}
      onOpen={(entryName) => push(`/skills/${encodeURIComponent(detailParams!.name)}/files/${encodeURIComponent(entryName)}`)}
    />
  {:else}
    <SkillsList onNavigate={(name) => push(`/skills/${encodeURIComponent(name)}`)} />
  {/if}
</div>
