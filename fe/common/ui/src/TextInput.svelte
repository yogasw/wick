<script lang="ts">
  /* Controlled themed text input. Consumes design-system tokens. */
  type Props = {
    value: string;
    onChange: (v: string) => void;
    type?: "text" | "email" | "url" | "password" | "search";
    placeholder?: string;
    disabled?: boolean;
    required?: boolean;
    id?: string;
    ariaLabel?: string;
    onBlur?: () => void;
    class?: string;
  };

  let {
    value,
    onChange,
    type = "text",
    placeholder,
    disabled = false,
    required = false,
    id,
    ariaLabel,
    onBlur,
    class: extraClass = "",
  }: Props = $props();

  const base =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 outline-none transition-colors focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 disabled:opacity-50";
</script>

<input
  {type}
  {id}
  {placeholder}
  {disabled}
  {required}
  aria-label={ariaLabel}
  class="{base} {extraClass}"
  value={value}
  oninput={(e) => onChange((e.target as HTMLInputElement).value)}
  onblur={() => onBlur?.()}
/>
