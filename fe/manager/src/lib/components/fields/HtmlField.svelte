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

  /* Timestamp of the last config write this widget's own action caused. The re-fetch
     effect ignores changes that land within the window below.

     A counter or a boolean is not enough: ConfigsForm.setFields assigns `values` once
     PER KEY with an await between them, so writing three fields produces three
     separate prop updates and three effect runs. A one-shot latch swallows the first
     and lets the rest through — which is exactly the bug where the result vanished
     while the inputs survived.

     Plain let, not $state: it is read inside the effect, and making it reactive would
     have the effect re-run when it changes. */
  let selfWriteAt = 0;
  const SELF_WRITE_WINDOW_MS = 1500;

  /* Timestamp of the last ANSWER an action produced. A plain render must not
     overwrite an answer that recent — see the guard in fetchHtml. */
  let actionAt = 0;

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
    if (Object.keys(map).length === 0) return;

    /* Suppress the re-fetch this write is about to trigger.

       Writing sibling fields updates `values` in the parent form, which flows back
       down as new props here and wakes the re-fetch effect — which then renders a
       fresh (empty) widget over the answer the op just returned.

       The first version of this guard only watched THIS widget's own field, on the
       assumption that an op writes the field it renders. That is not how ops
       actually behave: the test panel writes test_repo / test_remote / test_branch
       while rendering through test_panel, so the guard never fired and the result
       vanished anyway. Any write we caused counts. */
    selfWriteAt = Date.now();
    onSetFields(map);
  }

  /* Connector markup may carry its own <style> block. Re-inserting a stylesheet on
     every render makes the browser drop and re-apply it, so for one frame every
     element in the widget is unstyled — the flash people describe as "blinking".
     It is worst under `display: contents`, where the styles are the only thing
     holding the layout together.

     So the style block is split out of the markup and rendered ONCE, keyed on its
     own text: identical CSS across renders means Svelte leaves that node alone,
     and only the body swaps. A connector that changes its CSS still gets the new
     rules, because the key changes with the content. */
  const STYLE_RE = /<style\b[^>]*>([\s\S]*?)<\/style>/gi;

  function splitStyle(markup: string): { css: string; body: string } {
    let css = "";
    const body = markup.replace(STYLE_RE, (_m, inner: string) => {
      css += inner;
      return "";
    });
    return { css, body };
  }

  let html = $state("");

  // Derived, not assigned in three places: html stays the single source of truth,
  // so no code path can update the markup and forget to re-split it.
  let split = $derived(splitStyle(html));
  let styleCss = $derived(split.css);
  let bodyHtml = $derived(split.body);

  // rootEl wraps the connector HTML; used to read the values of any <input>/
  // <textarea> the connector rendered inside its own form.
  let rootEl: HTMLDivElement | undefined = $state();

  /* Patch the rendered markup in place instead of replacing it.

     Replacing the string behind {@html} throws away every node and builds new ones,
     which is what makes the panel visibly flash on each action — and it silently
     destroys anything the browser was holding: a half-typed input, the caret, which
     <details> was expanded, scroll position.

     This walks old and new trees together and only touches what actually differs.
     It is deliberately small — same-tag nodes get their attributes and text synced,
     anything structurally different is swapped wholesale — because a general-purpose
     morph library is far more than this needs and would be a new dependency.

     Inputs are a special case: their live `value` belongs to the operator, not to
     the server's markup, so it is preserved unless the element is genuinely new. */
  function morph(from: Element, toHTML: string): void {
    const tpl = document.createElement("div");
    tpl.innerHTML = toHTML;
    morphChildren(from, tpl);
  }

  function morphChildren(a: Element, b: Element): void {
    const an = Array.from(a.childNodes);
    const bn = Array.from(b.childNodes);

    for (let i = 0; i < bn.length; i++) {
      const want = bn[i];
      const have = an[i];
      if (!have) {
        a.appendChild(want.cloneNode(true));
        continue;
      }
      if (!sameShape(have, want)) {
        a.replaceChild(want.cloneNode(true), have);
        continue;
      }
      if (have.nodeType === Node.TEXT_NODE) {
        if (have.nodeValue !== want.nodeValue) have.nodeValue = want.nodeValue;
        continue;
      }
      if (have.nodeType === Node.ELEMENT_NODE) {
        morphElement(have as Element, want as Element);
      }
    }
    // Drop the tail the new markup no longer has.
    for (let i = an.length - 1; i >= bn.length; i--) a.removeChild(an[i]);
  }

  function sameShape(a: Node, b: Node): boolean {
    if (a.nodeType !== b.nodeType) return false;
    if (a.nodeType !== Node.ELEMENT_NODE) return true;
    const ae = a as Element;
    const be = b as Element;
    // id is how the connector marks a node as "the same one" across renders.
    return ae.tagName === be.tagName && ae.id === be.id;
  }

  function morphElement(have: Element, want: Element): void {
    // Sync attributes, but never clobber what the operator is typing.
    const isField = have instanceof HTMLInputElement || have instanceof HTMLTextAreaElement;
    for (const attr of Array.from(want.attributes)) {
      if (isField && attr.name === "value") continue;
      if (have.getAttribute(attr.name) !== attr.value) have.setAttribute(attr.name, attr.value);
    }
    for (const attr of Array.from(have.attributes)) {
      if (!want.hasAttribute(attr.name)) have.removeAttribute(attr.name);
    }
    // <details open> is operator state too: leave it as they left it.
    if (have instanceof HTMLDetailsElement && want instanceof HTMLDetailsElement) {
      // no-op: open state intentionally preserved
    }
    morphChildren(have, want);
  }

  /* Apply bodyHtml to the live DOM whenever it changes. The container itself is
     rendered empty by Svelte and owned by this effect, so Svelte never diffs its
     children and never replaces them out from under the morph. */
  $effect(() => {
    const body = bodyHtml; // track
    if (!rootEl) return;
    if (!rootEl.hasChildNodes()) {
      rootEl.innerHTML = body; // first paint: nothing to preserve
      return;
    }
    morph(rootEl, body);
  });
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
      // Only replace the markup when the op actually sent some. An op that returns
      // just { fields } is asking to fill sibling fields, not to blank itself out —
      // and clearing here emptied the widget for a frame before the next render
      // refilled it.
      /* Do not overwrite a result with a plain render.
         fetchHtml is the "render me" path; an action's response is an ANSWER. If a
         re-fetch is in flight when an action completes, or one slips past the guard
         above, the render must not land on top of the answer — that is the bug where
         the report appeared and then disappeared. actionAt marks the last answer. */
      if (r?.html !== undefined && Date.now() - actionAt >= SELF_WRITE_WINDOW_MS) {
        html = r.html;
      }
    } catch (e) {
      errorMsg = e instanceof Error ? e.message : String(e);
      // Keep whatever is on screen. The error renders above it, so the operator can
      // read what failed while still seeing the widget — clearing it threw away
      // working markup and flashed the panel empty.
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


  /* Collect the connector form's named field values. Any <input>/<textarea>/
     <select> with a `name` inside the widget becomes input.<name> sent to the op,
     so a connector can render its own form (e.g. a textarea to paste a cURL) and
     read what the user typed. data-arg (static) is still passed as `browser` for
     back-compat with the picker convention.

     Checkboxes and radios are the exception to reading `.value`: for those, value
     is the STATIC attribute the connector's markup declared, not the control's
     state. Reading it unconditionally submitted every box as ticked. The git
     connector's policy editor renders

         <input type="checkbox" name="g_force" value="true">

     and its save handler treats a non-empty g_force as "allowed", so unticking
     "Allow force push" and saving still wrote allow_force_push=true — the setting
     switched itself back on after every save. Follow the HTML form convention
     instead: an unchecked box contributes nothing, a checked one contributes its
     value (defaulting to "on", as a real form submission does). */
  function collectInputs(): Record<string, string> {
    const out: Record<string, string> = {};
    if (!rootEl) return out;
    rootEl
      .querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>("[name]")
      .forEach((f) => {
        const n = f.getAttribute("name");
        if (!n) return;
        const type = (f as HTMLInputElement).type;
        if (type === "checkbox" || type === "radio") {
          if ((f as HTMLInputElement).checked) {
            out[n] = f.getAttribute("value") ?? "on";
          }
          return;
        }
        out[n] = f.value;
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

    // UI-only markers (e.g. a kebab menu's <summary data-op="__menu">) must NOT
    // preventDefault — that would block the native <details> toggle — and must
    // not select the row. Return BEFORE preventDefault so the browser opens the
    // menu normally.
    if (opName.startsWith("__") && opName !== "__select") return;

    ev.preventDefault();
    if (opName === "__select") {
      // A picker DOES want the re-fetch here — the markup has to re-render so the
      // highlight moves to the new row — so this deliberately does not mark
      // selfSetValue.
      if (arg && arg !== value) onChange(arg);
      return;
    }
    if (busyOp) return; // one action at a time
    busyOp = opName;

    /* Mark the clicked element itself as busy.
       A connector action can take a while — cloning and pushing a repository is
       seconds, not milliseconds — and until now the only feedback was a line of
       small text below the widget. With nothing happening on the button, the
       natural reaction is to click again.

       data-busy is set on the element that was clicked, so the connector's own CSS
       decides what busy looks like (its markup, its styling). disabled is set too
       when the element supports it, which stops a second submission without any
       cooperation from the connector.

       Both are cleared in the finally below. The element can be replaced by the
       response's markup before then, which is fine: the replacement never carries
       these attributes. */
    const clicked = el as HTMLElement & { disabled?: boolean };
    clicked.setAttribute("data-busy", "true");
    const canDisable =
      clicked instanceof HTMLButtonElement ||
      clicked instanceof HTMLInputElement ||
      clicked instanceof HTMLSelectElement;
    if (canDisable) clicked.disabled = true;

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
        actionAt = Date.now();
        html = r.html;
        schedulePoll();
      } else {
        await fetchHtml();
      }
    } catch (e) {
      toastError("Action failed", e instanceof Error ? e.message : String(e));
    } finally {
      busyOp = "";
      clicked.removeAttribute("data-busy");
      if (canDisable) clicked.disabled = false;
    }
  }

  onDestroy(() => {
    if (pollTimer) clearTimeout(pollTimer);
  });

  /* Re-fetch when the selected value changes (so a picker's highlight follows the
     new selection) or on mount.

     The guard matters. An op may return { html, fields } together: the html is the
     answer it just computed, and the fields are values it wants persisted. Applying
     those fields changes `values` in the parent form, which flows back down as a new
     `value` here — and without the guard this effect then re-fetched, replacing the
     answer with a freshly rendered (typically empty) widget. Observed as three
     requests per click: the action, then two renders that wiped the operator's
     input.

     So: skip exactly one re-fetch when our own action wrote config. A change from
     anywhere else — the operator picking a different row, another field's write —
     still re-fetches, which is what pickers like loki's list_orgs and playwright's
     browser_status rely on for their highlight. */
  $effect(() => {
    void value; // track
    if (Date.now() - selfWriteAt < SELF_WRITE_WINDOW_MS) return;
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

<!-- html-widget scopes the busy rules at the bottom of this file: they have to be
     :global to reach markup that came from {@html}, so they are anchored to this
     class rather than applying page-wide. -->
<div class="html-widget rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3">
  <!-- The {@html} container is rendered UNCONDITIONALLY and only its content
       swaps. It used to sit inside {#if loading}{:else}…{/if}, which meant every
       state change destroyed the container and rebuilt it from scratch — the
       widget visibly flashed, and any form state inside it (a half-typed input,
       scroll position, which <details> was open) was lost with it.

       Status now renders as siblings that appear and disappear around a container
       that never moves. -->
  {#if loading && !html}
    <p class="text-xs text-black-700 dark:text-black-600">Loading…</p>
  {/if}
  {#if errorMsg}
    <p class="text-xs text-neg-400">Couldn't load: {errorMsg}</p>
    <button type="button" class="mt-1 text-xs text-green-500 hover:underline" onclick={() => fetchHtml()}>Retry</button>
  {/if}
  <!-- The connector's stylesheet, keyed on its own text so an unchanged sheet is
       left untouched across renders. Re-inserting it every time made the browser
       drop and re-apply the rules, leaving the widget unstyled for a frame — the
       flash that looked like the panel reloading. -->
  {#key styleCss}
    {#if styleCss}
      <!-- svelte:element rather than {@html "<style>…"} — a literal <style> tag
           inside a template string ends the component's own script parsing. -->
      <svelte:element this={"style"}>{styleCss}</svelte:element>
    {/if}
  {/key}
  <!-- Markup comes from the connector op. It is admin-only server content
       (not user input), and rendered inside the admin Settings page.

       Left EMPTY on purpose: the morph effect above owns these children. Using
       {@html} here would have Svelte replace the whole subtree on every change,
       which is the flash this component exists to avoid — and it would fight the
       morph for ownership of the same nodes. -->
  <div
    bind:this={rootEl}
    class="contents"
    onclick={onClick}
    role="presentation"
    aria-busy={busyOp ? "true" : "false"}
  ></div>
  <!-- Reserve the line's height at all times so showing and hiding the busy
       message cannot shift what is below it. -->
  <p class="mt-2 min-h-4 text-xs text-black-700 dark:text-black-600" aria-live="polite">
    {busyOp ? `Working… (${busyOp})` : ""}
  </p>
</div>

<style>
  /* Busy feedback for connector-rendered actions.

     Lives here rather than in each connector's markup for two reasons: a
     connector that styles inline (no <style> block of its own) has no way to
     express this at all, and every widget wants the same thing anyway — the
     element you clicked should look like it is working.

     onClick sets data-busy on the clicked element and clears it when the op
     settles, so these rules need no cooperation from the connector. :global is
     required because the elements come from {@html}, outside this component's
     scope. */
  .html-widget :global([data-busy]) {
    opacity: 0.65;
    cursor: progress;
    pointer-events: none;
  }

  /* A spinner ahead of the label, sized in em so it tracks whatever font the
     connector chose, and drawn in currentColor so it works on any button. */
  .html-widget :global([data-busy])::before {
    content: "";
    display: inline-block;
    width: 0.85em;
    height: 0.85em;
    margin-right: 0.45em;
    vertical-align: -0.1em;
    border: 2px solid currentColor;
    border-top-color: transparent;
    border-radius: 50%;
    animation: html-widget-spin 0.7s linear infinite;
  }

  @keyframes html-widget-spin {
    to {
      transform: rotate(360deg);
    }
  }

  /* Respect the OS setting: keep the dimming and the reserved space, drop the
     motion. */
  @media (prefers-reduced-motion: reduce) {
    .html-widget :global([data-busy])::before {
      animation: none;
    }
  }
</style>
