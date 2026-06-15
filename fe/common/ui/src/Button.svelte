<script lang="ts">
  /* Themed button with variant + size. Controlled via onclick; content via
     children snippet. Uses design-system tokens (consumes them; does not
     change the design-system). */
  import type { Snippet } from "svelte";

  type Variant = "primary" | "secondary" | "danger" | "ghost";
  type Size = "sm" | "md" | "lg";
  type Props = {
    variant?: Variant;
    size?: Size;
    type?: "button" | "submit";
    disabled?: boolean;
    title?: string;
    class?: string;
    onclick?: (e: MouseEvent) => void;
    children: Snippet;
  };

  let {
    variant = "primary",
    size = "md",
    type = "button",
    disabled = false,
    title,
    class: extraClass = "",
    onclick,
    children,
  }: Props = $props();

  const base =
    "rounded font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed";
  const variants: Record<Variant, string> = {
    primary: "bg-green-600 hover:bg-green-700 text-white-100",
    secondary: "bg-white-200 dark:bg-navy-700 hover:bg-white-300 dark:hover:bg-navy-600 text-black-800 dark:text-white-100",
    danger: "bg-rose-500 hover:bg-rose-600 text-white-100",
    ghost: "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700",
  };
  const sizes: Record<Size, string> = {
    sm: "px-2 py-1 text-xs",
    md: "px-3 py-1.5 text-xs",
    lg: "px-4 py-2 text-sm",
  };
</script>

<button
  {type}
  {title}
  {disabled}
  class="{base} {variants[variant]} {sizes[size]} {extraClass}"
  onclick={(e) => { if (!disabled) onclick?.(e); }}
>{@render children()}</button>
