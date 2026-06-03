<script lang="ts">
  import { route, match } from "$lib/router";
  import EditorShell from "$lib/components/workflow/EditorShell.svelte";

  const editParams = $derived(match("/edit/:id", $route));
</script>

{#if editParams}
  <EditorShell workflowID={editParams.id} />
{:else}
  <script>window.location.replace("/tools/agents/workflows");</script>
{/if}
