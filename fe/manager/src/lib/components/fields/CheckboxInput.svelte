<script lang="ts">
  /* Manager-local boolean widget. Stores "true"/"false" strings to match the
     config value contract. Rendered as an interactive toggle switch (not a bare
     checkbox) so a boolean setting reads as an on/off control. common-ui has no
     switch primitive, so this lives in the SPA. */
  type Props = {
    value: string;
    onChange: (v: string) => void;
    disabled?: boolean;
  };
  let { value, onChange, disabled = false }: Props = $props();
  let checked = $derived(value === "true");

  function toggle() {
    if (disabled) return;
    onChange(checked ? "false" : "true");
  }
</script>

<div class="inline-flex items-center gap-3 mt-1 select-none">
  <button
    type="button"
    role="switch"
    aria-checked={checked}
    {disabled}
    onclick={toggle}
    class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors
      focus:outline-none focus-visible:ring-2 focus-visible:ring-green-400 focus-visible:ring-offset-1
      disabled:cursor-not-allowed disabled:opacity-50
      {checked ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
  >
    <span
      class="inline-block h-4 w-4 transform rounded-full bg-white-100 shadow transition-transform
        {checked ? 'translate-x-4' : 'translate-x-0.5'}"
    ></span>
  </button>
  <button
    type="button"
    {disabled}
    onclick={toggle}
    class="text-xs cursor-pointer disabled:cursor-not-allowed {checked ? 'text-green-600 dark:text-green-400 font-medium' : 'text-black-800 dark:text-black-600'}"
  >{checked ? "Enabled" : "Disabled"}</button>
</div>
