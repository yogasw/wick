<script lang="ts">
  /* Manager-local date / datetime widget. common-ui has no date primitive.
     The `datetime` config type maps to the native datetime-local control. */
  type Props = {
    value: string;
    onChange: (v: string) => void;
    withTime?: boolean;
    disabled?: boolean;
  };
  let { value, onChange, withTime = false, disabled = false }: Props = $props();
  const base =
    "w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 outline-none transition-colors focus:border-green-500 disabled:opacity-50";
</script>

<input
  type={withTime ? "datetime-local" : "date"}
  {disabled}
  class={base}
  value={value}
  oninput={(e) => onChange((e.target as HTMLInputElement).value)}
/>
