<script lang="ts">
  /* Reasoning effort as the last step after a provider + model pick, for
     forms (project defaults, Team agent settings, failover targets, new
     session). Same gauge + slider as the composer. Asks the server what the
     picked model offers and hides itself when it offers nothing, so a model
     without effort never shows invented levels. */
  import EffortPicker from "./EffortPicker.svelte";
  import { effortPickerConfig, type EffortInfo } from "./effort-config.js";

  type Props = {
    provider: string;
    model: string;
    /** Saved effort ("" or "default" = the model default). */
    value: string;
    onChange: (value: string) => void;
    load: (provider: string, model: string) => Promise<EffortInfo>;
    id?: string;
    /** A saved default belongs to the provider + model it was picked for:
        changing either resets it to Default. Off for a one-off pick. */
    resetOnModelChange?: boolean;
    /** Label of the first stop, when "" means something other than the
        model default (a role inheriting its caller's effort). */
    defaultLabel?: string;
  };
  let { provider, model, value, onChange, load, id, resetOnModelChange = true, defaultLabel }: Props = $props();

  // The first pair seen is the stored one (a form filling in after load
  // goes from "" to it); only a later change of a set pair is the person's.
  let pair: string | undefined;
  $effect(() => {
    const next = `${provider}|${model}`;
    const prev = pair;
    pair = next;
    if (resetOnModelChange && prev !== undefined && !prev.startsWith("|") && prev !== next && value && value !== "default") {
      onChange("");
    }
  });

  let info = $state<EffortInfo | null>(null);
  let seq = 0;
  $effect(() => {
    const p = provider;
    const m = model;
    const n = ++seq;
    if (!p) {
      info = null;
      return;
    }
    // A loader that throws or returns nothing (a stubbed API) hides the
    // field instead of breaking the form around it.
    Promise.resolve()
      .then(() => load(p, m))
      .then((r) => {
        if (n === seq) info = r && Array.isArray(r.options) ? r : null;
      })
      .catch(() => {
        if (n === seq) info = null;
      });
  });

  const config = $derived.by(() => {
    const c = info ? effortPickerConfig({ ...info, requested: value === "default" ? "" : value }, (v) => onChange(v === "default" ? "" : v)) : undefined;
    if (!c || !defaultLabel) return c;
    return { ...c, defaultValue: "", options: c.options.map((o) => (o.id === "default" ? { ...o, label: defaultLabel } : o)) };
  });
  const current = $derived(config?.options.find((o) => o.id === config.value)?.label ?? "");
</script>

{#if config}
  <div {id} data-testid="effort-field" class="flex items-center justify-between gap-3 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-1.5">
    <div class="min-w-0">
      <div class="text-xs font-medium text-black-800 dark:text-black-600">Reasoning effort</div>
      <div class="truncate text-sm text-black-900 dark:text-white-100">{current}</div>
    </div>
    <EffortPicker {config} />
  </div>
  {#if resetOnModelChange}
    <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Resets to Default when the model changes.</p>
  {/if}
{/if}
