<script lang="ts">
  // Trigger node — first block in any workflow chain. Mirrors the
  // legacy editor's purple TRIGGER head + sub-label ("run / manual" or
  // "@cron / 0 */5 * * *"). Triggers are stored on
  // `workflow.triggers[]` rather than `graph.nodes`, but the canvas
  // shows them as cards just like nodes for layout consistency.
  import BaseNode from "./BaseNode.svelte";
  import type { Trigger } from "$lib/types/workflow";

  type Props = {
    trigger: Trigger;
    selected?: boolean;
    onselect?: () => void;
  };
  let { trigger, selected, onselect }: Props = $props();

  const sub = $derived.by(() => {
    switch (trigger.type) {
      case "cron":
        return trigger.expr ?? trigger.schedule ?? "—";
      case "channel":
        return [trigger.channel, trigger.event].filter(Boolean).join(" · ") || "—";
      case "webhook":
        return trigger.path ? `${trigger.method ?? "POST"} ${trigger.path}` : "—";
      case "schedule_at":
        return trigger.at ?? "—";
      default:
        return trigger.type;
    }
  });
</script>

<BaseNode
  id={trigger.id ?? `trigger-${trigger.type}`}
  type={"end" /* dummy — overridden by headLabel + headBg */}
  label={trigger.label ?? trigger.type}
  {selected}
  {onselect}
  headBg="#6366f1"
  headLabel="TRIGGER"
  inputs={0}
  outputs={1}
  icon="▸"
>
  {#snippet body()}
    <div class="text-[11px] text-black-700 dark:text-black-600 truncate">{sub}</div>
  {/snippet}
</BaseNode>
