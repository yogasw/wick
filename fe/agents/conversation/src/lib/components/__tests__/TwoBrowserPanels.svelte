<script lang="ts">
  import BrowserPanel from "../BrowserPanel.svelte";

  // The conversation mounts one panel per layout (side panel + mobile sheet)
  // and both bind the same view.
  let view = $state<"docked" | "float" | "max">("docked");
</script>

<div data-testid="desk"><BrowserPanel onError={() => {}} bind:view /></div>
<div data-testid="sheet"><BrowserPanel onError={() => {}} bind:view /></div>
<p data-testid="shared-view">{view}</p>
