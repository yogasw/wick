<!--
  Tools & features › Native tools + Bash commands. Edits a copy of the
  agent's switches and rules; the parent autosaves them like every tab.
-->
<script lang="ts">
  import { Toggle } from "@wick-fe/common-ui";
  import type { BashRule } from "../api/team.js";
  import { NATIVE_TOOLS, bashNote, bashPatternError, bashScopeError, enforcementNote, toggleTool } from "../nativeTools.js";

  type Props = {
    tools: string[];
    rules: BashRule[];
    enforced: boolean | undefined;
    provider: string;
    onTools: (t: string[]) => void;
    onRules: (r: BashRule[]) => void;
  };
  let { tools, rules, enforced, provider, onTools, onRules }: Props = $props();

  let pattern = $state("");
  let scope = $state("{project}");
  let touched = $state(false);
  const patternErr = $derived(touched ? bashPatternError(pattern) : "");
  const scopeErr = $derived(bashScopeError(scope));
  const bashOn = $derived(tools.includes("Bash"));
  const note = $derived(enforcementNote(enforced, provider));

  function add() {
    touched = true;
    if (bashPatternError(pattern) || scopeErr) return;
    onRules([...rules, { pattern: pattern.trim(), scope: scope.trim() }]);
    pattern = "";
    touched = false;
  }
  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
</script>

<section data-testid="native-tools">
  <p class="text-sm font-semibold text-black-900 dark:text-white-100">Native tools</p>
  <p class="mt-1 text-xs text-black-800 dark:text-black-600">The provider's own tools. Bash, Edit and Write change the machine and start off.</p>
  {#if note}
    <p class="mt-2 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-300" role="note" data-testid="tools-not-enforced">⚠ {note}</p>
  {/if}
  <div class="mt-3 space-y-3">
    {#each NATIVE_TOOLS as t (t.id)}
      <div class="flex items-start gap-3">
        <Toggle checked={tools.includes(t.id)} onChange={(v) => onTools(toggleTool(tools, t.id, v))} label={t.label} describedBy={`nt-${t.id}`} />
        <span class="min-w-0">
          <span class="block text-sm text-black-900 dark:text-white-100"><span aria-hidden="true">{t.icon}</span> {t.label}{#if t.risky}<span class="ml-1 text-xs font-medium text-red-600 dark:text-red-400">risky</span>{/if}</span>
          <span id="nt-{t.id}" class="block text-xs text-black-800 dark:text-black-600">{t.hint}</span>
        </span>
      </div>
    {/each}
  </div>
</section>

<section data-testid="bash-rules" class={bashOn ? "" : "opacity-60"}>
  <p class="text-sm font-semibold text-black-900 dark:text-white-100">Bash commands</p>
  <p class="mt-1 text-xs text-black-800 dark:text-black-600">{bashNote(bashOn, rules)} One command per rule; <code>*</code> at the end = any arguments. Scope <code>{"{project}"}</code> keeps path arguments inside the agent's project folder.</p>
  {#if rules.length > 0}
    <ul class="mt-2 divide-y divide-white-300 rounded-lg border border-white-300 dark:divide-navy-600 dark:border-navy-600">
      {#each rules as r, i (i)}
        <li class="flex items-center gap-2 px-3 py-2 text-sm">
          <code class="min-w-0 flex-1 truncate text-black-900 dark:text-white-100">{r.pattern}</code>
          <span class="shrink-0 text-xs text-black-800 dark:text-black-600">@ {r.scope || "{project}"}</span>
          <button type="button" class="shrink-0 rounded px-2 py-0.5 text-xs text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-navy-700" aria-label={`Remove ${r.pattern}`} onclick={() => onRules(rules.filter((_, j) => j !== i))}>Remove</button>
        </li>
      {/each}
    </ul>
  {/if}
  <form class="mt-2 flex flex-wrap items-start gap-2" onsubmit={(e) => { e.preventDefault(); add(); }}>
    <div class="min-w-0 flex-1">
      <input class={input} placeholder="git status" bind:value={pattern} aria-label="Command pattern" aria-invalid={!!patternErr} oninput={() => (touched = true)} disabled={!bashOn} />
      {#if patternErr}<p class="mt-1 text-xs text-red-600 dark:text-red-400" data-testid="bash-pattern-error">{patternErr}</p>{/if}
    </div>
    <div class="w-40">
      <input class={input} bind:value={scope} aria-label="Scope" aria-invalid={!!scopeErr} disabled={!bashOn} />
      {#if scopeErr}<p class="mt-1 text-xs text-red-600 dark:text-red-400">{scopeErr}</p>{/if}
    </div>
    <button type="submit" class="rounded-lg border border-white-300 px-3 py-2 text-sm font-medium text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700" disabled={!bashOn}>Add</button>
  </form>
</section>
