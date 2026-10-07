<script lang="ts">
  /* Themed on/off switch: the control several SPAs had been hand-rolling as
     the same twelve lines of inline markup (providers, project-settings).

     It lives here rather than in one SPA because the fe-module deduplication
     rule puts the second copy of a UI component in @wick-fe/common-ui. The
     existing inline copies are left alone deliberately — rewiring them is its
     own change, not a side effect of whichever feature needed the switch next.

     `describedBy` matters for the dangerous ones: a switch whose consequence
     is written next to it should point a screen reader at that sentence, not
     just at its own label. */
  type Props = {
    checked: boolean;
    onChange: (v: boolean) => void;
    label: string;
    disabled?: boolean;
    describedBy?: string;
    id?: string;
  };

  let { checked, onChange, label, disabled = false, describedBy, id }: Props = $props();
</script>

<button
  {id}
  type="button"
  role="switch"
  aria-checked={checked}
  aria-label={label}
  aria-describedby={describedBy}
  {disabled}
  onclick={() => onChange(!checked)}
  class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-green-200 dark:focus-visible:ring-green-800 disabled:cursor-not-allowed disabled:opacity-50 {checked
    ? 'bg-green-500'
    : 'bg-white-400 dark:bg-navy-600'}"
>
  <span
    class="inline-block h-4 w-4 rounded-full bg-white-100 transition-transform {checked
      ? 'translate-x-6'
      : 'translate-x-1'}"
  ></span>
</button>
