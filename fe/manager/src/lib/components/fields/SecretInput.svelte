<script lang="ts">
  /* Manager-local secret widget. The stored value is never sent down, so the
     input starts empty; a non-empty value here replaces the stored secret on
     save. hasValue drives the placeholder + the "stored" badge in the parent.
     Distinct from common-ui TextInput because of the password type +
     new-password autocomplete + replace-on-blank semantics. */
  type Props = {
    onChange: (v: string) => void;
    hasValue: boolean;
    disabled?: boolean;
  };
  let { onChange, hasValue, disabled = false }: Props = $props();
  const base =
    "w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm font-mono text-black-900 dark:text-white-100 outline-none transition-colors focus:border-green-500 disabled:opacity-50";
</script>

<input
  type="password"
  autocomplete="new-password"
  {disabled}
  placeholder={hasValue ? "Type new value to replace" : "Enter secret"}
  class={base}
  oninput={(e) => onChange((e.target as HTMLInputElement).value)}
/>
