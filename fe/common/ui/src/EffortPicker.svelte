<script lang="ts">
  import { mountEffortPicker, type EffortPickerConfig } from "./effort-picker.mjs";
  let { config }: {config: EffortPickerConfig} = $props();
</script>

<span use:mountEffortPicker={config} class="inline-flex shrink-0"></span>
