<script lang="ts">
  /* Manager-local color widget — native color picker plus the hex readout,
     mirroring the templ color field. */
  type Props = {
    value: string;
    onChange: (v: string) => void;
    disabled?: boolean;
  };
  let { value, onChange, disabled = false }: Props = $props();
</script>

<div class="flex items-center gap-3">
  <input
    type="color"
    {disabled}
    class="h-10 w-16 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 cursor-pointer p-1 disabled:opacity-50"
    value={value || "#000000"}
    oninput={(e) => onChange((e.target as HTMLInputElement).value)}
  />
  {#if value}
    <span class="font-mono text-xs text-black-800 dark:text-black-600">{value}</span>
  {/if}
</div>
