<script lang="ts">
  // Themed <select> wrapper — fixes the white-background-on-dark-mode
  // problem that native <select> has when using `bg-transparent`. Uses
  // `appearance-none` + an SVG chevron so the dropdown arrow is themed too.
  type Option = string | { label: string; value: string };

  type Props = {
    value: string;
    options: Option[];
    onChange: (v: string) => void;
    placeholder?: string;
    disabled?: boolean;
    class?: string;
    size?: "sm" | "md";
    /** "boxed" (default) is the bordered field; "minimal" is a borderless
        text+chevron control for toolbars. */
    variant?: "boxed" | "minimal";
  };

  let {
    value,
    options,
    onChange,
    placeholder,
    disabled = false,
    class: extraClass = "",
    size = "md",
    variant = "boxed",
  }: Props = $props();

  const base =
    "w-full appearance-none text-black-900 dark:text-white-100 outline-none transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed pr-7";
  const sizes = {
    sm: "px-2 py-1 text-xs",
    md: "px-3 py-2 text-sm",
  };
  const variants = {
    boxed: "rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800",
    minimal: "rounded-md border-0 bg-transparent font-medium hover:bg-white-200 dark:hover:bg-navy-700 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800",
  };

  function label(o: Option): string {
    return typeof o === "string" ? o : o.label;
  }
  function val(o: Option): string {
    return typeof o === "string" ? o : o.value;
  }
</script>

<div class="relative {extraClass}">
  <select
    class="{base} {sizes[size]} {variants[variant]}"
    {disabled}
    value={value}
    onchange={(e) => onChange((e.target as HTMLSelectElement).value)}
  >
    {#if placeholder}
      <option value="">{placeholder}</option>
    {/if}
    {#each options as o}
      <option value={val(o)}>{label(o)}</option>
    {/each}
  </select>
  <!-- Themed chevron — replaces browser's native arrow -->
  <div class="pointer-events-none absolute inset-y-0 right-2 flex items-center text-black-700 dark:text-black-600">
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
      <polyline points="6 9 12 15 18 9"/>
    </svg>
  </div>
</div>
