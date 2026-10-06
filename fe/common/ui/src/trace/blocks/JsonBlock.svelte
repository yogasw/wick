<script lang="ts">
  import JsonTree from "../../JsonTree.svelte";
  import CodeBlock from "./CodeBlock.svelte";
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();

  // A json body the trace cut mid-way no longer parses — show it as json
  // code (the banner explains why) instead of an error or nothing.
  const parsed = $derived.by<{ ok: boolean; value?: unknown }>(() => {
    try {
      return { ok: true, value: JSON.parse(display.body ?? "") };
    } catch {
      return { ok: false };
    }
  });
</script>

{#if parsed.ok}
  <div data-trace-kind="json" class="px-2 py-1.5"><JsonTree value={parsed.value} /></div>
{:else}
  <CodeBlock {display} lang="json" />
{/if}
