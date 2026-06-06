<script lang="ts">
  // JSON preview — live draft (what the canvas is showing right now)
  // side-by-side with the last published copy. Reactive: any edit on
  // the canvas refreshes the left pane immediately; the right stays
  // pinned to the published version until the user hits Publish.
  import { draftWorkflow, publishedWorkflow, dirty } from "$lib/stores/editor";
  import JsonDiff from "../fields/JsonDiff.svelte";

  // Show full workflow JSON including `_canvas` positions so
  // operators can confirm node drags actually persist. This is what
  // the backend writes to disk verbatim (minus YAML formatting).
  const draftText = $derived(
    $draftWorkflow ? JSON.stringify($draftWorkflow, null, 2) : "",
  );
  const publishedText = $derived(
    $publishedWorkflow ? JSON.stringify($publishedWorkflow, null, 2) : "",
  );
</script>

<JsonDiff
  leftText={draftText}
  rightText={publishedText}
  leftLabel="Live (draft)"
  rightLabel="Published"
  rightEmptyMsg="No published version yet. Publish the draft to populate this pane."
>
  {#snippet note()}
    {#if $dirty}
      <span class="text-amber-600 dark:text-amber-400">●&nbsp;unpublished changes</span>
    {:else}
      <span class="text-black-700 dark:text-black-600">in sync</span>
    {/if}
  {/snippet}
</JsonDiff>
