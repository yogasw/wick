<script lang="ts">
  /* Controlled themed number input. Consumes design-system tokens. */
  type Props = {
    value: number;
    onChange: (v: number) => void;
    min?: number;
    max?: number;
    step?: number;
    placeholder?: string;
    disabled?: boolean;
    required?: boolean;
    id?: string;
    ariaLabel?: string;
    class?: string;
  };

  let {
    value,
    onChange,
    min,
    max,
    step,
    placeholder,
    disabled = false,
    required = false,
    id,
    ariaLabel,
    class: extraClass = "",
  }: Props = $props();

  const base =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 outline-none transition-colors focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 disabled:opacity-50";
</script>

<input
  type="number"
  {id}
  {min}
  {max}
  {step}
  {placeholder}
  {disabled}
  {required}
  aria-label={ariaLabel}
  class="{base} {extraClass}"
  value={value}
  oninput={(e) => onChange(Number((e.target as HTMLInputElement).value) || 0)}
/>
