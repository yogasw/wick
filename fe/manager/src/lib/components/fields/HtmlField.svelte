<script lang="ts">
  /* Generic server-rendered config widget (type "html").

     The CORE stays domain-agnostic: it fetches markup from a connector op
     (field.options = the op key), renders it read-only, and wires one thin
     convention so the connector's own HTML can drive behaviour —

       data-op="<opKey>" data-arg="<value>"
         → run that op via the manager /test path, then re-fetch this HTML
       data-op="__select" data-arg="<value>"
         → store <value> as this field's value (reserved, no op call)

     An op may ALSO return { fields: { key: value } } alongside (or instead of)
     { html }. When present, those key/value pairs are written to sibling config
     fields via onSetFields — e.g. a "paste a cURL → Extract" button that fills
     token_v2 + user-agent + version in one click. The connector decides which
     keys; the core only applies known ones. 

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
    /** apply many sibling fields when an op returns { fields: {...} } */
    onSetFields?: (map: Record<string, string>) => void;
  };
  let { connectorKey, connectorId, op, value, disabled = false, onChange, onSetFields }: Props = $props();

  /** Pull a { fields: {k:v} } map off an op response and apply it (known keys
      only — enforced upstream in ConfigsForm.setFields). Ignores anything that
      isn't a flat string map. */
  function applyFields(resp: unknown): void {
    if (!onSetFields || !resp || typeof resp !== "object") return;
    const f = (resp as { fields?: unknown }).fields;
    if (!f || typeof f !== "object") return;
    const map: Record<string, string> = {};
    for (const [k, v] of Object.entries(f as Record<string, unknown>)) {
      if (typeof v === "string") map[k] = v;
    }
    if (Object.keys(map).length > 0) onSetFields(map);
  }

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
      const r = res.response as { html?: string; fields?: unknown } | undefined;
      applyFields(r);
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

  // rootEl wraps the connector HTML; used to read the values of any <input>/
  // <textarea> the connector rendered inside its own form.
  let rootEl: HTMLDivElement | undefined = $state();

  /* Collect the connector form's named field values. Any <input>/<textarea>/
     <select> with a `name` inside the widget becomes input.<name> sent to the op,
     so a connector can render its own form (e.g. a textarea to paste a cURL) and
     read what the user typed. data-arg (static) is still passed as `browser` for
     back-compat with the picker convention. */
  function collectInputs(): Record<string, string> {
    const out: Record<string, string> = {};
    if (!rootEl) return out;
    rootEl
      .querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>("[name]")
      .forEach((f) => {
        const n = f.getAttribute("name");
        if (n) out[n] = f.value;
      });
    return out;
  }

  /* Delegated click handler: find the nearest element carrying data-op and act.
     __select just stores the value (single-field picker convention). Any other
     op runs via /test with { browser: data-arg, ...named form inputs }. The op's
     response drives the widget: { fields } writes sibling config fields, { html }
     replaces the markup (so the connector renders its own feedback — a validation
     error, a success note, whatever). If it returns neither html nor fields we
     re-fetch, preserving the old picker/progress behaviour. */
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
      const input = { browser: arg, ...collectInputs() };
      const res = await runConnectorTest(connectorKey, connectorId, opName, input, "");
      if (res.error) throw new Error(res.error);
      const r = res.response as { html?: string; fields?: unknown } | undefined;
      // Apply any config fields the op asked us to set.
      applyFields(r);
      // If the op returned its own HTML (feedback/validation), show it as-is;
      // otherwise re-fetch (picker/progress flows return neither).
      if (r?.html !== undefined) {
        html = r.html;
        schedulePoll();
      } else {
        await fetchHtml();
      }
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

  // Auto-select when the fetched markup offers exactly ONE selectable option and
  // nothing is chosen yet — so a single-choice picker (e.g. one Grafana org)
  // fills itself instead of forcing a pointless click. Guarded to the empty
  // state so it never overrides an operator's existing pick, and it only ever
  // stores a value the connector's own HTML advertised via data-op="__select".
  $effect(() => {
    if (disabled || value || !html) return;
    const opts = Array.from(
      document.createRange().createContextualFragment(html).querySelectorAll<HTMLElement>('[data-op="__select"]'),
    );
    if (opts.length === 1) {
      const only = opts[0].dataset.arg ?? "";
      if (only) onChange(only);
    }
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
    <div bind:this={rootEl} class="contents" onclick={onClick} role="presentation">{@html html}</div>
    {#if busyOp}
      <p class="mt-2 text-xs text-black-700 dark:text-black-600">Working… ({busyOp})</p>
    {/if}
  {/if}
</div>
