<script lang="ts">
  /* A panel read that came back with a reason instead of data.

     This renders as something to switch on, not as an error: the two reasons
     the server names — the backend's web API being off, and the daemon being
     down — are settings, which is why they arrive with HTTP 200. An empty
     table here would say "you have no memory", which is the opposite of what
     happened (PLAN §13.1, §13.5 point 5). */
  import { Button } from "@wick-fe/common-ui";
  import type { Blocked } from "./format.js";

  type Props = { blocked: Blocked; onAction?: () => void };
  let { blocked, onAction }: Props = $props();
</script>

<div
  class="rounded-xl border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-8 text-center"
>
  <svg
    viewBox="0 0 16 16"
    class="mx-auto h-8 w-8 text-black-700 dark:text-black-600"
    fill="none"
    stroke="currentColor"
    stroke-width="1.25"
    aria-hidden="true"
  >
    <circle cx="8" cy="8" r="6.25"></circle>
    <path d="M8 4.75v4" stroke-linecap="round"></path>
    <circle cx="8" cy="11.25" r="0.5" fill="currentColor"></circle>
  </svg>
  <h2 class="mt-3 text-sm font-medium text-black-900 dark:text-white-100">{blocked.title}</h2>
  <p class="mx-auto mt-1.5 max-w-lg text-xs leading-relaxed text-black-700 dark:text-black-600">{blocked.body}</p>
  {#if blocked.action && onAction}
    <div class="mt-4">
      <Button variant="primary" size="md" onclick={onAction}>{blocked.action}</Button>
    </div>
  {/if}
</div>
