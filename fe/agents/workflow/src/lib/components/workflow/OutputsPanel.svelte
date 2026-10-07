<script lang="ts">
  // Outputs of a case-routed node, n8n style: each output port, what it
  // means and where it goes. Branch/classify outputs can be added, renamed
  // and removed here (a rename retags its edges, a remove drops them);
  // fixed outputs (datatable found/not_found, default) are read-only.
  import type { Node } from "$lib/types/workflow";
  import { draftWorkflow, detailNodeID, renameNodeCase, removeNodeCase } from "$lib/stores/editor";
  import { outputPorts, portTone, hasEditablePorts, fallbackPort } from "./ports";

  type Props = { node: Node; onCasesChange: (next: string[]) => void };
  let { node, onCasesChange }: Props = $props();

  const edges = $derived($draftWorkflow?.graph?.edges ?? []);
  const ports = $derived(outputPorts(node, edges));
  const editable = $derived(hasEditablePorts(node.type));
  const fallback = $derived(fallbackPort(node));
  const labelOf = (id: string) => $draftWorkflow?.graph?.nodes?.find((n) => n.id === id)?.label ?? id;
  const targets = (c: string) => edges.filter((e) => e.from === node.id && e.case === c).map((e) => e.to);

  const hint = $derived.by(() => {
    switch (node.type) {
      case "branch": return `The expression's output picks the output with the same name; anything else goes to ${fallback}.`;
      case "classify": return `The model picks one of these outputs; anything else goes to ${fallback}.`;
      case "switch": return `Outputs come from the rules above; no matching rule goes to ${fallback}.`;
      case "datatable_get": return "found when the row exists, not_found otherwise.";
      case "datatable_exists": return "true when a matching row exists, false otherwise.";
      default: return "";
    }
  });

  let draft = $state("");
  let error = $state("");

  // Declared outputs = every port but the fallback, so a legacy branch
  // whose outputs only lived on edges becomes declared on its first edit.
  function declared(): string[] {
    return ports.filter((p) => p !== fallback);
  }

  function invalid(name: string, except?: string): string {
    if (!name) return "Name is required.";
    if (name === fallback) return `"${fallback}" is the fallback output.`;
    if (name !== except && ports.includes(name)) return `"${name}" already exists.`;
    return "";
  }

  function add() {
    const name = draft.trim();
    error = invalid(name);
    if (error) return;
    onCasesChange([...declared(), name]);
    draft = "";
  }

  function rename(prev: string, raw: string) {
    const next = raw.trim();
    if (next === prev) return;
    error = invalid(next, prev);
    if (error) return;
    onCasesChange(declared().map((p) => (p === prev ? next : p)));
    renameNodeCase(node.id, prev, next);
  }

  function remove(name: string) {
    const n = targets(name).length;
    if (n > 0 && !window.confirm(`Remove output "${name}" and its ${n} connection${n === 1 ? "" : "s"}?`)) return;
    error = "";
    onCasesChange(declared().filter((p) => p !== name));
    removeNodeCase(node.id, name);
  }
</script>

<div class="space-y-2" data-testid="outputs-panel">
  <div>
    <div class="text-xs font-medium">Outputs</div>
    {#if hint}<p class="text-[11px] text-black-700 dark:text-black-600">{hint}</p>{/if}
  </div>
  <div class="rounded border border-white-400 dark:border-navy-600 divide-y divide-white-400 dark:divide-navy-600">
    {#each ports as p (p)}
      {@const to = targets(p)}
      <div class="flex items-center gap-2 px-2 py-1.5 text-[12px]" data-testid="output-row">
        {#if editable && p !== fallback}
          <input
            class="w-36 shrink-0 rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-0.5 font-mono text-[12px]"
            value={p}
            aria-label="Output name"
            onchange={(e) => rename(p, (e.target as HTMLInputElement).value)}
            onkeydown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          />
        {:else}
          <span class="w-36 shrink-0 truncate rounded px-2 py-0.5 font-mono {portTone(p)}" title={p}>{p}</span>
        {/if}
        <span class="text-black-700 dark:text-black-600">→</span>
        <div class="flex min-w-0 flex-1 flex-wrap gap-1">
          {#if to.length === 0}
            <span class="text-[11px] italic text-rose-600 dark:text-rose-400">not connected — drag from its port on the canvas</span>
          {:else}
            {#each to as id}
              <button
                type="button"
                class="truncate font-mono text-emerald-600 hover:underline dark:text-emerald-400"
                onclick={() => detailNodeID.set(id)}
                title="Open {id}"
              >{labelOf(id)}</button>
            {/each}
          {/if}
        </div>
        {#if editable && p !== fallback}
          <button
            type="button"
            class="shrink-0 rounded px-1.5 text-black-700 hover:bg-rose-100 hover:text-rose-700 dark:text-black-600 dark:hover:bg-rose-500/20 dark:hover:text-rose-300"
            onclick={() => remove(p)}
            title="Remove output"
            aria-label="Remove output {p}"
          >✕</button>
        {/if}
      </div>
    {/each}
  </div>
  {#if editable}
    <div class="flex items-center gap-2">
      <input
        class="flex-1 rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 font-mono text-[12px]"
        placeholder="new output name"
        bind:value={draft}
        onkeydown={(e) => e.key === "Enter" && add()}
      />
      <button
        type="button"
        class="rounded border border-white-400 dark:border-navy-600 px-2 py-1 text-[12px] hover:bg-white-300 dark:hover:bg-navy-600"
        onclick={add}
      >+ Add output</button>
    </div>
  {/if}
  {#if error}<p class="text-[11px] text-rose-600 dark:text-rose-400">{error}</p>{/if}
</div>
