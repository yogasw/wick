<script lang="ts">
  import { route, match, push } from "$lib/router";
  import EditorShell from "$lib/components/workflow/EditorShell.svelte";
  import WorkflowList from "$lib/components/workflow/WorkflowList.svelte";

  // Resolve current route — first match wins. Hash-only so the Go side
  // serves one HTML shell + the SPA owns navigation client-side.
  const editParams = $derived(match("/edit/:id", $route));
</script>

{#if editParams}
  <EditorShell workflowID={editParams.id} />
{:else}
  <WorkflowList onpick={(id) => push(`/edit/${id}`)} />
{/if}
