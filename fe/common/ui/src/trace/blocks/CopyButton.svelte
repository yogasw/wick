<script lang="ts">
  import { copyText } from "../../clipboard.js";
  type Props = { text: string; label?: string };
  let { text, label = "Copy" }: Props = $props();
  let done = $state(false);

  async function copy(e: Event): Promise<void> {
    e.stopPropagation();
    if (await copyText(text)) {
      done = true;
      setTimeout(() => (done = false), 1200);
    }
  }
</script>

<button type="button" data-trace-copy onclick={copy} disabled={!text}
  class="rounded px-1.5 py-0.5 text-[10px] font-medium text-black-600 dark:text-black-500 hover:bg-white-200 dark:hover:bg-navy-700 disabled:opacity-40"
>{done ? "Copied" : label}</button>
