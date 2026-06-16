<script lang="ts">
  /* Manager-local boolean widget. Stores "true"/"false" strings to match the
     config value contract. common-ui has no checkbox primitive, so this lives
     in the SPA. */
  type Props = {
    value: string;
    onChange: (v: string) => void;
    disabled?: boolean;
  };
  let { value, onChange, disabled = false }: Props = $props();
  let checked = $derived(value === "true");
</script>

<label class="inline-flex items-center gap-3 cursor-pointer mt-1 select-none">
  <input
    type="checkbox"
    {disabled}
    checked={checked}
    class="w-4 h-4 accent-green-500 cursor-pointer rounded"
    onchange={(e) => onChange((e.target as HTMLInputElement).checked ? "true" : "false")}
  />
  <span class="text-xs text-black-800 dark:text-black-600">{checked ? "Enabled" : "Disabled"}</span>
</label>
