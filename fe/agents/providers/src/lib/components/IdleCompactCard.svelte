<script lang="ts">
  import { Button, Select } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { apiSaveConfigKey, apiIdleCompactProbe, type IdleCompactProbe } from "$lib/api.js";
  import type { ConfigFieldDTO } from "$lib/types.js";
  import { parseRules, formatRules, emptyCond, type Rule, type RuleField } from "$lib/idlerules.js";

  /* Compact-when-idle has its own card because it is not admin-only: a
     manager (whoever may reconnect the instance) may tune it too, and
     every key saves on its own endpoint, which the API allows for them. */
  type Props = {
    base: string;
    type: string;
    name: string;
    fields: ConfigFieldDTO[];
    onSaved?: () => void;
  };
  let { base, type, name, fields, onSaved }: Props = $props();

  const LABELS: Record<string, string> = {
    idle_compact_seconds: "Idle before compact (seconds)",
    idle_compact_trigger: "Compact trigger",
    idle_compact_threshold: "Compact threshold (% or tokens)",
    idle_compact_scope: "Which sessions",
    idle_compact_match: "Session patterns for the scope",
  };

  const PLACEHOLDERS: Record<string, string> = {
    idle_compact_match: "id:rest-\nid:wf_adhoc_\nproject:support\ntitle:/^daily-/",
  };

  let values = $state<Record<string, string>>({});
  let saving = $state(false);
  $effect(() => {
    const next: Record<string, string> = {};
    for (const f of fields) next[f.Key] = f.Value;
    values = next;
    rules = parseRules(next["idle_compact_match"] ?? "");
  });

  /* Rule builder over idle_compact_match: one rule per line, its
     conditions all required. "Edit as text" shows the raw lines. */
  let rules = $state<Rule[]>([]);
  let textMode = $state(false);
  const FIELD_OPTS: { v: RuleField; label: string }[] = [
    { v: "project", label: "project name" },
    { v: "project_id", label: "project id" },
    { v: "title", label: "title" },
    { v: "id", label: "session id" },
    { v: "", label: "any of them" },
  ];
  function syncRules() {
    values["idle_compact_match"] = formatRules(rules);
  }
  function addRule() {
    rules.push({ conds: [emptyCond()] });
  }
  function addCond(ri: number) {
    rules[ri].conds.push(emptyCond());
  }
  function removeCond(ri: number, ci: number) {
    rules[ri].conds.splice(ci, 1);
    if (rules[ri].conds.length === 0) rules.splice(ri, 1);
    syncRules();
  }
  function setTextMode(on: boolean) {
    if (!on) rules = parseRules(values["idle_compact_match"] ?? "");
    textMode = on;
  }

  /* Tester: paste a session link and see what the rules in the form
     (saved or not) would do with it. */
  let probeInput = $state("");
  let probing = $state(false);
  let probe = $state<IdleCompactProbe | null>(null);
  let probeError = $state("");
  async function runProbe() {
    if (!probeInput.trim()) return;
    probing = true;
    probeError = "";
    probe = null;
    try {
      const scope = values["idle_compact_scope"] || "skip";
      probe = await apiIdleCompactProbe(base, type, name, probeInput.trim(), scope, values["idle_compact_match"] ?? "");
    } catch (e) {
      probeError = e instanceof Error ? e.message : "Test failed";
    } finally {
      probing = false;
    }
  }
  function ruleLabel(p: IdleCompactProbe, scope: string): string {
    if (scope === "all") return "Scope is all, every session counts.";
    if (p.rule === 0) return scope === "whitelist" ? "No rule matched." : "No skip rule matched.";
    return `Rule ${p.rule} matched: ${p.rule_text ?? ""}`;
  }

  let toggle = $derived(fields.find((f) => f.Key === "idle_compact"));
  let enabled = $derived(values["idle_compact"] === "true");
  // The pattern list means nothing when the scope is all.
  let details = $derived(
    fields.filter((f) => f.Key !== "idle_compact" && !(f.Key === "idle_compact_match" && values["idle_compact_scope"] === "all")),
  );

  async function setEnabled(on: boolean) {
    values["idle_compact"] = on ? "true" : "false";
    try {
      await apiSaveConfigKey(base, type, name, "idle_compact", values["idle_compact"]);
      toastOk(on ? "Compact when idle is on" : "Compact when idle is off");
      onSaved?.();
    } catch (e) {
      values["idle_compact"] = on ? "false" : "true";
      toastError(e instanceof Error ? e.message : "Save failed");
    }
  }

  /* Number fields read with a thousands dot as they are typed (100.000)
     and are saved without it. */
  const GROUPED = new Set(["idle_compact_seconds", "idle_compact_threshold"]);
  function ungroup(v: string): string {
    return v.replace(/[.,\s]/g, "");
  }
  function group(v: string): string {
    const m = /^(\d+)(%?)$/.exec(ungroup(v));
    return m ? m[1].replace(/\B(?=(\d{3})+(?!\d))/g, ".") + m[2] : v;
  }
  $effect(() => {
    for (const k of GROUPED) {
      const v = values[k];
      if (v && group(v) !== v) values[k] = group(v);
    }
  });

  async function saveDetails() {
    saving = true;
    try {
      for (const f of details) {
        const v = values[f.Key] ?? "";
        await apiSaveConfigKey(base, type, name, f.Key, GROUPED.has(f.Key) ? ungroup(v) : v);
      }
      toastOk("Idle compact saved");
      onSaved?.();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Save failed");
    } finally {
      saving = false;
    }
  }
</script>

{#if toggle}
  <div data-testid="idle-compact-card" class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 space-y-4">
    <div class="flex items-start justify-between gap-4">
      <div>
        <p class="text-sm font-semibold text-black-900 dark:text-white-100">Compact when idle</p>
        <p class="mt-1 text-[11px] text-black-700 dark:text-black-600 leading-relaxed">{toggle.Description}</p>
      </div>
      <button
        type="button"
        role="switch"
        aria-label="idle_compact"
        data-testid="idle-compact-toggle"
        aria-checked={enabled}
        onclick={() => setEnabled(!enabled)}
        class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors {enabled ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
      >
        <span class="inline-block h-5 w-5 transform rounded-full bg-white-100 shadow transition-transform {enabled ? 'translate-x-5' : 'translate-x-0.5'}"></span>
      </button>
    </div>
    {#if enabled}
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-5">
        {#each details as f (f.Key)}
          <div class={f.Type === "textarea" ? "sm:col-span-2" : ""}>
            <div class="flex items-center gap-2 mb-1.5">
              <span class="font-mono text-xs font-semibold text-black-900 dark:text-white-100">{f.Key}</span>
              {#if LABELS[f.Key]}<span data-testid={`field-label-${f.Key}`} class="text-xs text-black-700 dark:text-black-600">{LABELS[f.Key]}</span>{/if}
            </div>
            {#if (f.Type === "dropdown" || f.Type === "select") && f.Options}
              <Select
                ariaLabel={f.Key}
                value={values[f.Key] ?? ""}
                options={f.Options.split(f.Type === "dropdown" ? "|" : ",").map((o) => o.trim()).filter(Boolean)}
                onChange={(v) => { values[f.Key] = v; }}
              />
            {:else if f.Key === "idle_compact_match"}
              <div class="space-y-2" data-testid="idle-rules">
                <div class="flex justify-end">
                  <button type="button" class="text-[11px] text-green-600 hover:underline" onclick={() => setTextMode(!textMode)}>
                    {textMode ? "Use rule builder" : "Edit as text"}
                  </button>
                </div>
                {#if textMode}
                  <textarea
                    aria-label={f.Key}
                    bind:value={values[f.Key]}
                    rows="4"
                    spellcheck="false"
                    placeholder={PLACEHOLDERS[f.Key] ?? ""}
                    class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-xs font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
                  ></textarea>
                {:else}
                  {#if rules.length === 0}
                    <p class="text-[11px] text-black-700 dark:text-black-600">
                      No rules yet.{values["idle_compact_scope"] === "whitelist" ? " With whitelist nothing is compacted until a rule is added." : " With skip the default leaves out one-way REST calls and one-off workflow runs."}
                    </p>
                  {/if}
                  {#each rules as rule, ri}
                    {#if ri > 0}<p class="text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600 pl-1">or</p>{/if}
                    <div data-testid="idle-rule" class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-200/40 dark:bg-navy-800 p-2.5 space-y-1.5">
                      <div class="text-[10px] font-semibold text-black-700 dark:text-black-600">Rule {ri + 1}</div>
                      {#each rule.conds as cond, ci}
                        <div class="flex flex-wrap items-center gap-1.5">
                          <span class="w-8 text-[10px] font-semibold uppercase text-black-700 dark:text-black-600">{ci === 0 ? "if" : "and"}</span>
                          <select aria-label="field" bind:value={cond.field} onchange={syncRules} class="rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 text-xs">
                            {#each FIELD_OPTS as o}<option value={o.v}>{o.label}</option>{/each}
                          </select>
                          <select
                            aria-label="operator"
                            value={`${cond.neg ? "not" : "is"}-${cond.regex ? "re" : "has"}`}
                            onchange={(e) => {
                              const v = (e.currentTarget as HTMLSelectElement).value;
                              cond.neg = v.startsWith("not");
                              cond.regex = v.endsWith("re");
                              syncRules();
                            }}
                            class="rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 text-xs"
                          >
                            <option value="is-has">contains</option>
                            <option value="not-has">does not contain</option>
                            <option value="is-re">matches regex</option>
                            <option value="not-re">does not match regex</option>
                          </select>
                          <input
                            aria-label="value"
                            bind:value={cond.value}
                            oninput={syncRules}
                            placeholder={cond.regex ? "^daily-" : "ygsw"}
                            class="min-w-[8rem] flex-1 rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 text-xs font-mono outline-none focus:border-green-500"
                          />
                          <button type="button" aria-label="remove condition" class="px-1.5 text-xs text-black-700 hover:text-red-500" onclick={() => removeCond(ri, ci)}>✕</button>
                        </div>
                      {/each}
                      <button type="button" class="text-[11px] text-green-600 hover:underline" onclick={() => addCond(ri)}>+ and condition</button>
                    </div>
                  {/each}
                  <button type="button" data-testid="idle-rule-add" class="text-xs text-green-600 hover:underline" onclick={addRule}>+ add rule (or)</button>
                  {#if values[f.Key]}
                    <pre class="mt-1 whitespace-pre-wrap rounded-md bg-white-200 dark:bg-navy-800 px-2 py-1.5 text-[10px] font-mono text-black-700 dark:text-black-600">{values[f.Key]}</pre>
                  {/if}
                {/if}
              </div>
            {:else if f.Type === "textarea"}
              <textarea
                aria-label={f.Key}
                bind:value={values[f.Key]}
                rows="3"
                spellcheck="false"
                placeholder={PLACEHOLDERS[f.Key] ?? ""}
                class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-xs font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
              ></textarea>
            {:else}
              <input
                type={f.Type === "number" && !GROUPED.has(f.Key) ? "number" : "text"}
                inputmode={GROUPED.has(f.Key) ? "numeric" : undefined}
                aria-label={f.Key}
                bind:value={values[f.Key]}
                class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
              />
            {/if}
            {#if f.Description}
              <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600 leading-relaxed whitespace-pre-line">{f.Description}</p>
            {/if}
          </div>
        {/each}
      </div>
      <div class="flex justify-end">
        <Button variant="primary" onclick={saveDetails} disabled={saving}>{saving ? "Saving…" : "Save idle compact"}</Button>
      </div>
    {/if}

    <div data-testid="idle-compact-test" class="border-t border-white-300 dark:border-navy-600 pt-4 space-y-2">
      <p class="text-xs font-semibold text-black-900 dark:text-white-100">Test a session</p>
      <p class="text-[11px] text-black-700 dark:text-black-600">Paste a session link or id to see what the rules above do with it. Unsaved changes are used, and it works while compact when idle is off.</p>
      <div class="flex gap-2">
        <input
          aria-label="session to test"
          bind:value={probeInput}
          onkeydown={(e) => { if (e.key === "Enter") runProbe(); }}
          placeholder="https://…/tools/agents/sessions/<id>"
          class="flex-1 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-xs font-mono outline-none focus:border-green-500"
        />
        <Button variant="secondary" onclick={runProbe} disabled={probing || !probeInput.trim()}>{probing ? "Testing…" : "Test"}</Button>
      </div>
      {#if probeError}
        <p class="text-xs text-red-500">{probeError}</p>
      {/if}
      {#if probe}
        <div data-testid="idle-compact-probe" class="rounded-lg border px-3 py-2.5 text-xs space-y-1 {probe.in_scope ? 'border-green-400 bg-green-50 dark:bg-green-900/20' : 'border-white-400 dark:border-navy-600'}">
          <p class="font-semibold {probe.in_scope ? 'text-green-700 dark:text-green-300' : 'text-black-900 dark:text-white-100'}">
            {probe.in_scope ? "Will be compacted when idle" : "Left alone, never compacted"}
          </p>
          <p class="text-black-700 dark:text-black-600">{probe.title || "(no title)"} · project {probe.project || "(none)"}{probe.project_id ? ` (${probe.project_id})` : ""} · <span class="font-mono">{probe.session_id}</span></p>
          <p class="text-black-700 dark:text-black-600">{ruleLabel(probe, values["idle_compact_scope"] || "skip")}</p>
          {#if probe.in_scope && !probe.enabled}
            <p class="text-amber-600">Compact when idle is off for this instance, so nothing happens until it is turned on.</p>
          {/if}
          {#if probe.session_provider && !probe.same_provider}
            <p class="text-amber-600">This session last ran on {probe.session_provider}, so that instance's own settings decide.</p>
          {/if}
          {#if probe.in_scope && probe.context_window}
            <p class="text-black-700 dark:text-black-600">
              Context now {Math.round(((probe.context_used ?? 0) / probe.context_window) * 100)}% ({Math.round((probe.context_used ?? 0) / 1000)}k / {Math.round(probe.context_window / 1000)}k) · {probe.over_threshold ? "past the threshold, compacted once it sits idle long enough" : "below the threshold, not compacted yet"}
            </p>
          {/if}
        </div>
      {/if}
    </div>
  </div>
{/if}
