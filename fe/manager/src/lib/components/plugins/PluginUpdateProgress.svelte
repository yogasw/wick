<script lang="ts">
  /* Progress bar for a streaming plugin update (updatePluginStream). Shared by
     the connector detail header and the Admin → Plugins Installed rows so both
     show the same phases. Renders nothing for the error phase — the caller
     toasts the failure. */
  import { ProgressBar } from "@wick-fe/common-ui";
  import type { PluginProgress } from "$lib/api.js";
  import { pluginPhaseLabel } from "./updateProgress.js";

  type Props = { progress: PluginProgress; class?: string };
  let { progress, class: klass = "" }: Props = $props();

  const phaseLabel = $derived(pluginPhaseLabel(progress));
  // Unknown download size streams pct -1 → indeterminate bar.
  const pct = $derived(progress.phase === "downloading" ? progress.pct : 100);
</script>

{#if progress.phase !== "error"}
  <ProgressBar class={klass} {pct} label={phaseLabel} />
{/if}
