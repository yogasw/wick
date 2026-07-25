<script lang="ts">
  import { onMount } from "svelte";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { apiGetWickModelKey, type WickCurlForms } from "$lib/api";

  // Curl builder: reconstruct one model call as a copyable request. Fixes
  // the old single-string output that got truncated pasting into Postman —
  // now the operator picks a format (single-line / bash / raw HTTP / JSON
  // body), chooses how the bearer token is rendered (placeholder / custom /
  // the real key), edits the body if needed, and copies whole or per-part.
  type Props = {
    base: string;
    forms: WickCurlForms | null;
    onClose: () => void;
  };
  let { base, forms, onClose }: Props = $props();

  type Format = "single_line" | "raw_http" | "multiline" | "json_body";
  let format = $state<Format>("single_line");

  const FORMAT_LABELS: Record<Format, string> = {
    single_line: "Single-line",
    raw_http: "Raw HTTP",
    multiline: "Bash",
    json_body: "JSON body",
  };

  // Bearer: how the token appears in the output. "placeholder" keeps
  // $WICK_MODEL_API_KEY; "custom" uses whatever the operator types; the Get
  // button fetches the real key (admin) into the custom field.
  let bearerMode = $state<"placeholder" | "custom">("placeholder");
  let customToken = $state("");
  let revealing = $state(false);

  const PLACEHOLDER = "$WICK_MODEL_API_KEY";

  // The base text for the current format, with the chosen bearer swapped in.
  const baseText = $derived.by(() => {
    if (!forms) return "";
    let t = forms[format] ?? "";
    if (bearerMode === "custom" && customToken.trim()) {
      t = t.split(PLACEHOLDER).join(customToken.trim());
    }
    return t;
  });

  // Editable working copy. Reset to baseText whenever the format/bearer
  // changes (a fresh render), but let the operator tweak it before copying.
  let edited = $state("");
  let dirty = $state(false);
  $effect(() => {
    // Re-derive when baseText changes UNLESS the operator has edited — then
    // keep their text until they explicitly Reset.
    const t = baseText;
    if (!dirty) edited = t;
  });

  function reset() {
    edited = baseText;
    dirty = false;
  }

  async function getRealKey() {
    if (!forms?.model_id) return;
    revealing = true;
    try {
      const key = await apiGetWickModelKey(base, forms.model_id);
      if (!key) {
        toastError("No key stored for this model");
        return;
      }
      customToken = key;
      bearerMode = "custom";
      toastOk("Real key loaded into the token field");
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Could not reveal the key");
    } finally {
      revealing = false;
    }
  }

  async function copy(text: string, what: string) {
    try {
      await navigator.clipboard.writeText(text);
      toastOk(`${what} copied`);
    } catch {
      toastError("Clipboard unavailable");
    }
  }

  // Env vars to export before running (base_url, key name). Shown so the
  // operator knows what to set — especially with the placeholder token.
  const envEntries = $derived.by(() =>
    forms?.env ? Object.entries(forms.env) : ([] as [string, string][]),
  );
  // Copy form: shell-comment lines, so pasting into a script is harmless.
  const envCopyText = $derived(envEntries.map(([k, v]) => `# ${k}: ${v}`).join("\n"));

  onMount(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
</script>

{#if forms}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center p-4"
    role="button"
    tabindex="-1"
    onclick={onClose}
    onkeydown={(e) => e.key === "Enter" && onClose()}
  >
    <div
      class="flex max-h-[85vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label="Copy as curl"
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.stopPropagation()}
    >
      <div class="flex items-center gap-3 border-b border-white-300 dark:border-navy-600 px-5 py-3">
        <div class="min-w-0 flex-1">
          <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Copy request</h3>
          <p class="text-[11px] text-black-700 dark:text-black-600">Pick a format, set the token, edit if needed, then copy.</p>
        </div>
        <button onclick={onClose} aria-label="Close" class="rounded-lg p-1 text-black-600 hover:bg-white-200 dark:hover:bg-navy-700">
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path></svg>
        </button>
      </div>

      <div class="flex flex-col gap-3 overflow-auto px-5 py-4">
        <!-- Format -->
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-[11px] font-medium text-black-700 dark:text-black-600 w-16">Format</span>
          <div class="inline-flex overflow-hidden rounded-lg border border-white-300 dark:border-navy-600">
            {#each Object.keys(FORMAT_LABELS) as f (f)}
              <button
                onclick={() => { format = f as Format; dirty = false; }}
                class={`px-2.5 py-1 text-xs font-medium ${format === f ? "bg-green-500 text-white-100" : "text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700"}`}
              >{FORMAT_LABELS[f as Format]}</button>
            {/each}
          </div>
          {#if format === "single_line"}
            <span class="text-[10px] text-black-600 dark:text-black-500">← paste-safe for Postman</span>
          {/if}
        </div>

        <!-- Bearer token (hidden for JSON-body-only) -->
        {#if format !== "json_body"}
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-[11px] font-medium text-black-700 dark:text-black-600 w-16">Token</span>
            <div class="inline-flex overflow-hidden rounded-lg border border-white-300 dark:border-navy-600">
              <button onclick={() => (bearerMode = "placeholder")} class={`px-2.5 py-1 text-xs font-medium ${bearerMode === "placeholder" ? "bg-green-500 text-white-100" : "text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700"}`}>Placeholder</button>
              <button onclick={() => (bearerMode = "custom")} class={`px-2.5 py-1 text-xs font-medium ${bearerMode === "custom" ? "bg-green-500 text-white-100" : "text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700"}`}>Custom</button>
            </div>
            {#if bearerMode === "custom"}
              <input
                bind:value={customToken}
                placeholder="paste token…"
                class="min-w-0 flex-1 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 font-mono text-xs text-black-900 dark:text-white-100"
              />
            {/if}
            <button
              onclick={getRealKey}
              disabled={revealing}
              title="Load this model's real API key (admin)"
              class="rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700 disabled:opacity-50"
            >{revealing ? "…" : "Get real key"}</button>
          </div>
          {#if bearerMode === "placeholder"}
            <p class="pl-16 text-[10px] text-black-600 dark:text-black-500">Output keeps <code>{PLACEHOLDER}</code> — export it before running.</p>
          {/if}
        {/if}

        <!-- Env hint -->
        {#if envEntries.length > 0 && format !== "json_body"}
          <div class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 px-3 py-2">
            <div class="mb-1.5 flex items-center justify-between">
              <span class="text-[10px] font-semibold uppercase tracking-wide text-black-800 dark:text-black-600">Env</span>
              <button onclick={() => copy(envCopyText, "Env")} class="text-[10px] font-medium text-green-600 hover:text-green-700 dark:text-green-400">Copy env</button>
            </div>
            <div class="flex flex-col gap-1">
              {#each envEntries as [k, v] (k)}
                <div class="flex items-baseline gap-2 font-mono text-[11px]">
                  <span class="shrink-0 font-medium text-black-900 dark:text-white-100">{k}</span>
                  <span class="min-w-0 flex-1 truncate text-black-800 dark:text-black-600" title={v}>{v}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}

        <!-- Editable output -->
        <div>
          <div class="mb-1 flex items-center justify-between">
            <span class="text-[10px] font-medium uppercase tracking-wide text-black-600 dark:text-black-500">
              {format === "json_body" ? "Body" : "Request"}{dirty ? " · edited" : ""}
            </span>
            {#if dirty}
              <button onclick={reset} class="text-[10px] text-black-600 hover:text-black-800 dark:hover:text-black-400">Reset</button>
            {/if}
          </div>
          <textarea
            bind:value={edited}
            oninput={() => (dirty = true)}
            spellcheck="false"
            class="h-56 w-full resize-y rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 font-mono text-[11px] leading-relaxed text-black-900 dark:text-white-100"
          ></textarea>
        </div>
      </div>

      <div class="flex items-center justify-end gap-2 border-t border-white-300 dark:border-navy-600 px-5 py-3">
        <button onclick={onClose} class="rounded-lg px-3 py-1.5 text-xs font-medium text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700">Close</button>
        <button onclick={() => copy(edited, format === "json_body" ? "Body" : "Request")} class="rounded-lg bg-green-500 px-4 py-1.5 text-xs font-semibold text-white-100 hover:bg-green-600">Copy</button>
      </div>
    </div>
  </div>
{/if}
