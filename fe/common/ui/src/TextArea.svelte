<script lang="ts">
  /* Controlled themed textarea. Consumes design-system tokens. */
  type Props = {
    value: string;
    onChange: (v: string) => void;
    rows?: number;
    placeholder?: string;
    disabled?: boolean;
    required?: boolean;
    id?: string;
    maxlength?: number;
    ariaLabel?: string;
    class?: string;
  };

  let {
    value,
    onChange,
    rows = 4,
    placeholder,
    disabled = false,
    required = false,
    id,
    maxlength,
    ariaLabel,
    class: extraClass = "",
  }: Props = $props();

  const base =
    "w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm font-mono text-black-900 dark:text-white-100 outline-none transition-colors focus:border-green-500 disabled:opacity-50";
</script>

<textarea
  {rows}
  {id}
  {placeholder}
  {disabled}
  {required}
  {maxlength}
  aria-label={ariaLabel}
  class="{base} {extraClass}"
  value={value}
  oninput={(e) => onChange((e.target as HTMLTextAreaElement).value)}
></textarea>
