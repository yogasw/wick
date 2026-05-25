<script lang="ts">
  import { draftWorkflow } from "$lib/stores/editor";

  // Plain JSON dump for now — server endpoint can stringify YAML when
  // needed. JSON is more useful for AI/copy-paste flows and matches the
  // svelte-migration goal of JSON-everywhere.
  const text = $derived(JSON.stringify($draftWorkflow, null, 2));
</script>

<pre class="font-mono text-[11px] whitespace-pre-wrap leading-tight">{text}</pre>
