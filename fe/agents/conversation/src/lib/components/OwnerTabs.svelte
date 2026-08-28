<script lang="ts">
  /* Two-tab scope switch: the caller's sessions vs everyone's. One control
     shared by the sessions list and the board's untracked rail, so the two
     places spell the same choice the same way. The tab only names the
     scope — whoever renders the data prints its count, which is how "count
     on click" works: each tab's total arrives with that tab's fetch. */
  type Props = {
    value: "me" | "all";
    onChange: (v: "me" | "all") => void;
    mineLabel?: string;
    allLabel?: string;
    /* "xs" shrinks the control for tight spots (the untracked rail). */
    size?: "sm" | "xs";
  };

  let {
    value,
    onChange,
    mineLabel = "Your sessions",
    allLabel = "All sessions",
    size = "sm",
  }: Props = $props();

  const btnPad = $derived(size === "xs" ? "px-2 py-0.5 text-[10px]" : "px-4 py-1.5 text-xs");
</script>

<div class="inline-flex overflow-hidden rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
  <button
    type="button"
    aria-pressed={value === "me"}
    data-testid="owner-tab-me"
    onclick={() => value !== "me" && onChange("me")}
    class={btnPad + " font-medium transition-colors " + (value === "me"
      ? "bg-green-500 text-white-100"
      : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600")}
  >{mineLabel}</button>
  <button
    type="button"
    aria-pressed={value === "all"}
    data-testid="owner-tab-all"
    onclick={() => value !== "all" && onChange("all")}
    class={btnPad + " font-medium transition-colors " + (value === "all"
      ? "bg-green-500 text-white-100"
      : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600")}
  >{allLabel}</button>
</div>
