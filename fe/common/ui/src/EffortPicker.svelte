<script lang="ts">
  import { mountEffortPicker, type EffortPickerConfig } from "./effort-picker.mjs";
  let { config }: {config: EffortPickerConfig} = $props();
</script>

<!-- Theme tokens cross the shadow boundary as custom properties, so the
     panel follows the composer palette in light, dark and named themes. -->
<span use:mountEffortPicker={config} class="effort-host inline-flex shrink-0"></span>

<style>
  .effort-host {
    --ep-panel: rgb(var(--color-white-100));
    --ep-border: rgb(var(--color-white-300));
    --ep-text: rgb(var(--color-black-900));
    --ep-muted: rgb(var(--color-black-700));
    --ep-track: rgb(var(--color-white-300));
    --ep-dot: rgb(0 0 0 / 0.2);
    --ep-accent: rgb(40 131 114);
  }
  :global(.dark) .effort-host {
    --ep-panel: rgb(var(--color-navy-800));
    --ep-border: rgb(var(--color-navy-600));
    --ep-text: rgb(var(--color-white-100));
    --ep-muted: rgb(var(--color-black-600));
    --ep-track: rgb(var(--color-navy-600));
    --ep-dot: rgb(255 255 255 / 0.3);
    --ep-accent: rgb(110 197 178);
  }
</style>
