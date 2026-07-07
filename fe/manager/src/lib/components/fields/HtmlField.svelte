<script lang="ts">
  /* Generic server-rendered config widget (type "html").

     The CORE stays domain-agnostic: it fetches markup from a connector op
     (field.options = the op key), renders it read-only, and wires one thin
     convention so the connector's own HTML can drive behaviour —

       data-op="<opKey>" data-arg="<value>"
         → run that op via the manager /test path, then re-fetch this HTML
       data-op="__select" data-arg="<value>"
         → store <value> as this field's value (reserved, no op call)

     All layout, buttons, badges, and per-item logic live in the connector's
     HTML — never here. So the same widget serves a browser picker, a model
     list, a region chooser, etc., with zero widget changes. */
  import { onDestroy } from "svelte";
  import { runConnectorTest } from "$lib/api.js";
  import { toastError } from "@wick-fe/common-stores";

  type Props = {
    connectorKey: string;
    connectorId: string;
    /** op key that returns { html: "..." } — from field.options */
    op: string;
    /** current stored value of the field */
    value: string;
    disabled?: boolean;
    /** persist a new value when the HTML selects one */
    onChange: (v: string) => void;
  };
  let { connectorKey, connectorId, op, value, disabled = false, onChange }: Props = $props();

  let html = $state("");
  let loading = $state(true);
  let busyOp = $state(""); // op currently running (drives a spinner on its button)
  let errorMsg = $state("");
  let pollTimer: ReturnType<typeof setTimeout> | undefined;

  async function fetchHtml(): Promise<void> {
    if (!op || !connectorId) return;
    // Only show the "Loading…" placeholder on the FIRST fetch (no HTML yet).
    // A poll-driven refresh (progress bar advancing) already has HTML on screen
    // — swapping it for "Loading…" every 1.2s makes the whole widget flicker.
    // Keep the current markup up until the new markup arrives, then swap.
    if (!html) loading = true;
    errorMsg = "";
    try {
      const res = await runConnectorTest(connectorKey, connectorId, op, { browser: value }, "");
      if (res.error) throw new Error(res.error);
      const r = res.response as { html?: string } | undefined;
      html = r?.html ?? "";
    } catch (e) {
      errorMsg = e instanceof Error ? e.message : String(e);
      html = "";
    } finally {
      loading = false;
      schedulePoll();
    }
  }

  /* While the returned HTML advertises an in-flight action (any element with
     data-installing), keep re-fetching so a progress bar advances. The op HTML
     drops the marker when it's done, which stops the loop. This is how "live
     progress" works without SSE — the widget polls, the backend reports state
     via the status op. */
  function schedulePoll(): void {
    if (pollTimer) clearTimeout(pollTimer);
    if (html.includes("data-installing")) {
      pollTimer = setTimeout(() => fetchHtml(), 1200);
    }
  }

  /* Delegated click handler: find the nearest element carrying data-op and act
     on its data-arg. __select just stores the value; any other op is run via
     /test (with the arg passed as `browser`, matching the plugin's input), then
     the HTML is re-fetched so freshly-installed items flip state. */
  async function onClick(ev: MouseEvent): Promise<void> {
    if (disabled) return;
    const el = (ev.target as HTMLElement | null)?.closest<HTMLElement>("[data-op]");
    if (!el) return;
    const opName = el.dataset.op ?? "";
    const arg = el.dataset.arg ?? "";
    if (!opName) return;
    ev.preventDefault();

    if (opName === "__select") {
      if (arg && arg !== value) onChange(arg);
      return;
    }
    if (busyOp) return; // one action at a time
    busyOp = opName;
    try {
      const res = await runConnectorTest(connectorKey, connectorId, opName, { browser: arg }, "");
      if (res.error) throw new Error(res.error);
      // The op may run async (e.g. a long download that returns {started:true}).
      // Re-fetch immediately; schedulePoll then keeps polling while the status
      // HTML shows a data-installing progress row.
      await fetchHtml();
    } catch (e) {
      toastError("Action failed", e instanceof Error ? e.message : String(e));
    } finally {
      busyOp = "";
    }
  }

  onDestroy(() => {
    if (pollTimer) clearTimeout(pollTimer);
  });

  // Re-fetch when the selected value changes (so the highlight follows) or on mount.
  $effect(() => {
    void value; // track
    fetchHtml();
  });
</script>

<div class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3">
  {#if loading}
    <p class="text-xs text-black-700 dark:text-black-600">Loading…</p>
  {:else if errorMsg}
    <p class="text-xs text-neg-400">Couldn't load: {errorMsg}</p>
    <button type="button" class="mt-1 text-xs text-green-500 hover:underline" onclick={() => fetchHtml()}>Retry</button>
  {:else}
    <!-- Markup comes from the connector op. It is admin-only server content
         (not user input), and rendered inside the admin Settings page. -->
    <!-- eslint-disable-next-line svelte/no-at-html-tags -->
    <div class="contents" onclick={onClick} role="presentation">{@html html}</div>
    {#if busyOp}
      <p class="mt-2 text-xs text-black-700 dark:text-black-600">Working… ({busyOp})</p>
    {/if}
  {/if}
</div>
