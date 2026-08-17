<script lang="ts">
  // One process's command line: truncated inline, expandable in place.
  //
  // A `title` attribute alone was not enough. The native tooltip waits
  // about a second, cannot be selected, and browsers clip it — which is
  // worst for exactly the long arguments that made someone hover in the
  // first place. Clicking opens the full text where it can be read and
  // copied.

  import { middleTruncate } from "$lib/format.js";

  interface Props {
    cmd: string;
  }
  let { cmd }: Props = $props();

  let open = $state(false);
  let copied = $state(false);

  // Shortened from the MIDDLE, not the end. Every Chrome helper shares the
  // same long path to the same binary, and what tells a renderer from a
  // GPU process is the --type= argument last in the line — clip the tail
  // and every row reads identically.
  const short = $derived(middleTruncate(cmd));

  async function copy(): Promise<void> {
    try {
      await navigator.clipboard.writeText(cmd);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      // Clipboard access can be refused (insecure origin, permissions).
      // The text is selectable either way, so this is not worth an error.
    }
  }
</script>

<div class="mt-0.5">
  <button
    type="button"
    class="block w-full overflow-hidden whitespace-nowrap text-left font-mono text-[10px] text-black-600 transition-colors hover:text-blue-600 dark:text-black-700 dark:hover:text-blue-400"
    title={open ? "Hide full command" : cmd}
    aria-expanded={open}
    onclick={() => (open = !open)}
  >
    {short}
  </button>

  {#if open}
    <!-- Inline rather than a floating popover: a popover over a table that
         refreshes every 10s would be repositioned or dismissed out from
         under the reader. This stays put. -->
    <div
      class="mt-1 rounded-md border border-white-300 bg-white-200 p-2 dark:border-navy-600 dark:bg-navy-800"
    >
      <p class="select-text break-all font-mono text-[10px] leading-relaxed text-black-900 dark:text-white-100">
        {cmd}
      </p>
      <button
        type="button"
        class="mt-1 text-[10px] text-blue-600 hover:underline dark:text-blue-400"
        onclick={() => void copy()}
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  {/if}
</div>
