<script lang="ts">
  import { mountEffortPicker, type EffortPickerConfig } from "./effort-picker.mjs";
  let { config }: {config: EffortPickerConfig} = $props();
  let host: HTMLSpanElement;
  $effect(() => {
    const control = mountEffortPicker(host, config);
    return () => control.destroy();
  });
</script>

<span bind:this={host} class="inline-flex shrink-0"></span>
