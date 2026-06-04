<script lang="ts">
  // Workflow-wide search palette. Opens via the top-right magnifier
  // button or Ctrl/Cmd+K. Two modes:
  //
  //   • Quick — labels / ids / types only. Fast scan; covers the
  //     "which node was that called again" case.
  //   • Deep — adds node config (URL, prompt, command, args …) +
  //     captured run inputs/outputs from stepResultsByNode. Lets
  //     debug queries like "which step returned 'abc'" land directly
  //     on the producing node.
  //
  // Each result is a row showing the node label + a snippet of the
  // matched text with the query highlighted. Click navigates the
  // user: nodes open the inspector, triggers open the trigger modal.
  import { draftWorkflow, searchOpen, detailNodeID, detailTriggerID, stepResultsByNode, selectedNodeID } from "$lib/stores/editor";

  type Hit = {
    kind: "node" | "trigger";
    id: string;
    label: string;
    typeOrSub: string;
    matchedField: string;
    snippet: string;
  };

  let query = $state("");
  let mode = $state<"quick" | "deep">("quick");
  let activeIdx = $state(0);
  let inputEl: HTMLInputElement | undefined = $state();

  // Reset + focus whenever the overlay opens. Cursor lands in the
  // input so the user can start typing immediately.
  $effect(() => {
    if ($searchOpen) {
      query = "";
      activeIdx = 0;
      // requestAnimationFrame so the <input> is attached before focus
      requestAnimationFrame(() => inputEl?.focus());
    }
  });

  // Stringify only the fields that matter for a deep search — strips
  // _canvas positions and other UI noise so a "0" coordinate doesn't
  // light up half the graph. Pulls the run snapshot for the same node
  // so output text ("name": "abc") is searchable too.
  function deepHaystackFor(node: any): string {
    const copy: Record<string, unknown> = { ...node };
    delete copy._canvas;
    delete copy.id;
    delete copy.label;
    delete copy.type;
    const run = $stepResultsByNode[node.id];
    if (run) {
      if (run.input) copy.__input = run.input;
      if (run.output) copy.__output = run.output;
    }
    try {
      return JSON.stringify(copy);
    } catch {
      return "";
    }
  }

  // Build the hit list per query keystroke. Cheap — workflows rarely
  // exceed a few dozen nodes, so a linear scan with `includes` is
  // plenty. Snippet windows around the first match so the user sees
  // why a row matched without opening the inspector.
  const hits = $derived.by<Hit[]>(() => {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    const wf = $draftWorkflow;
    if (!wf) return [];
    const out: Hit[] = [];

    const tryHit = (
      kind: "node" | "trigger",
      id: string,
      label: string,
      typeOrSub: string,
      candidates: Array<{ field: string; text: string }>,
    ) => {
      for (const c of candidates) {
        const idx = c.text.toLowerCase().indexOf(q);
        if (idx < 0) continue;
        out.push({
          kind,
          id,
          label,
          typeOrSub,
          matchedField: c.field,
          snippet: snippetAround(c.text, idx, q.length),
        });
        return;
      }
    };

    for (const n of wf.graph?.nodes ?? []) {
      const candidates: Array<{ field: string; text: string }> = [
        { field: "label", text: n.label ?? "" },
        { field: "id", text: n.id ?? "" },
        { field: "type", text: n.type ?? "" },
      ];
      if (mode === "deep") {
        candidates.push({ field: "content", text: deepHaystackFor(n) });
      }
      tryHit("node", n.id, n.label || n.id, n.type, candidates);
    }
    for (const t of wf.triggers ?? []) {
      const candidates: Array<{ field: string; text: string }> = [
        { field: "label", text: t.label ?? "" },
        { field: "id", text: t.id ?? "" },
        { field: "type", text: t.type ?? "" },
      ];
      if (mode === "deep") {
        const { ...rest } = t as any;
        try {
          candidates.push({ field: "content", text: JSON.stringify(rest) });
        } catch {
          /* ignore — non-serializable */
        }
      }
      tryHit(
        "trigger",
        t.id ?? "",
        t.label || t.id || t.type,
        `${t.type}${(t as any).channel ? `:${(t as any).channel}` : ""}`,
        candidates,
      );
    }
    return out;
  });

  function snippetAround(text: string, idx: number, qlen: number): string {
    const W = 30;
    const start = Math.max(0, idx - W);
    const end = Math.min(text.length, idx + qlen + W);
    return (start > 0 ? "…" : "") + text.slice(start, end) + (end < text.length ? "…" : "");
  }

  // Renders a snippet with the query span highlighted. Plain string +
  // CSS rather than dangerouslySetInnerHTML so query strings with
  // markup-looking chars stay literal.
  type SnippetPart = { text: string; match: boolean };
  function snippetParts(snippet: string): SnippetPart[] {
    const q = query.trim();
    if (!q) return [{ text: snippet, match: false }];
    const parts: SnippetPart[] = [];
    let i = 0;
    const lower = snippet.toLowerCase();
    const ql = q.toLowerCase();
    while (i < snippet.length) {
      const at = lower.indexOf(ql, i);
      if (at < 0) {
        parts.push({ text: snippet.slice(i), match: false });
        break;
      }
      if (at > i) parts.push({ text: snippet.slice(i, at), match: false });
      parts.push({ text: snippet.slice(at, at + q.length), match: true });
      i = at + q.length;
    }
    return parts;
  }

  function close() {
    searchOpen.set(false);
  }

  function pick(h: Hit) {
    close();
    if (h.kind === "node") {
      selectedNodeID.set(h.id);
      detailNodeID.set(h.id);
    } else {
      detailTriggerID.set(h.id);
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      close();
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      activeIdx = Math.min(activeIdx + 1, Math.max(0, hits.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      activeIdx = Math.max(0, activeIdx - 1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const h = hits[activeIdx];
      if (h) pick(h);
    }
  }

  // Clamp the active index whenever the result set shrinks past it
  // (typing narrows the list).
  $effect(() => {
    if (activeIdx >= hits.length) activeIdx = Math.max(0, hits.length - 1);
  });
</script>

{#if $searchOpen}
  <!-- absolute inset-0 (not fixed) so this overlay anchors to the
       Canvas component it's mounted under — the palette ends up
       centred on the canvas surface itself, not drifting over the
       wick sidebar or the right panel chrome. Fixed-size 480px tall
       so the box doesn't snap shorter when the result list empties
       (user noticed the size jumping between empty and matched). -->
  <div
    class="absolute inset-0 z-[70] bg-white-100 dark:bg-navy-800/40 flex items-center justify-center p-4"
    onclick={close}
    role="presentation"
  >
    <div
      class="rounded-xl bg-white dark:bg-navy-800 border border-slate-200 dark:border-navy-600 shadow-2xl flex flex-col overflow-hidden"
      style="width: min(560px, 92%); height: min(480px, 92%);"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.stopPropagation()}
      role="dialog"
      tabindex="-1"
      aria-label="Search workflow"
    >
      <header class="p-3 border-b border-slate-200 dark:border-navy-600">
        <div class="flex items-center gap-2">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="text-black-700 dark:text-black-500 shrink-0"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/></svg>
          <input
            bind:this={inputEl}
            bind:value={query}
            onkeydown={onKey}
            placeholder={mode === "deep" ? "Search labels, ids, configs, outputs…" : "Search node labels…"}
            class="flex-1 bg-transparent outline-none text-sm text-slate-900 dark:text-white-100 placeholder:text-black-700 dark:text-black-500"
          />
          <span class="text-[10px] text-black-700 dark:text-black-500">Esc to close</span>
        </div>
        <!-- Quick / Deep mode tabs. Deep mode opts into config +
             run-output searches that are heavier but find the rows
             "Quick" can't (e.g. which node returned 'abc'). -->
        <div class="mt-2 flex items-center gap-1 text-[11px]">
          {#each [
            { key: "quick", label: "Quick" },
            { key: "deep", label: "Deep" },
          ] as t}
            <button
              class="px-2 py-0.5 rounded transition-colors"
              class:bg-emerald-500={mode === t.key}
              class:text-white-100={mode === t.key}
              class:text-black-700={mode !== t.key}
              class:hover:text-slate-900={mode !== t.key}
              onclick={() => (mode = t.key as typeof mode)}
            >{t.label}</button>
          {/each}
          <span class="ml-auto text-black-700 dark:text-black-500">
            {hits.length} {hits.length === 1 ? "match" : "matches"}
          </span>
        </div>
      </header>

      <ul class="flex-1 overflow-y-auto min-h-0">
        {#if !query.trim()}
          <li class="px-4 py-6 text-xs text-black-700 dark:text-black-600 italic">
            Type to search. Quick covers labels / ids / types; Deep also scans node configs + captured run outputs.
          </li>
        {:else if hits.length === 0}
          <li class="px-4 py-6 text-xs text-black-700 dark:text-black-600 italic">
            No matches for "{query}".
          </li>
        {:else}
          {#each hits as h, i (h.kind + ":" + h.id + ":" + h.matchedField)}
            <li>
              <button
                type="button"
                class="w-full text-left px-3 py-2 border-b border-slate-100 dark:border-navy-600 transition-colors"
                class:bg-emerald-50={activeIdx === i}
                class:bg-navy-700={activeIdx === i}
                onclick={() => pick(h)}
                onmouseenter={() => (activeIdx = i)}
              >
                <div class="flex items-center gap-2">
                  <span
                    class="px-1.5 py-0.5 rounded text-[10px] uppercase tracking-wider shrink-0"
                    class:bg-sky-500={h.kind === "trigger"}
                    class:text-white-100={h.kind === "trigger" || h.kind === "node"}
                    class:bg-white-200={h.kind === "node"}
                    class:bg-navy-600={h.kind === "node"}
                    class:text-black-700={h.kind === "node"}
                  >
                    {h.kind}
                  </span>
                  <span class="font-mono text-xs truncate text-slate-900 dark:text-white-100">{h.label}</span>
                  <span class="text-[10px] text-black-700 dark:text-black-500">{h.typeOrSub}</span>
                  <span class="ml-auto text-[10px] text-black-700 dark:text-black-500 uppercase tracking-wider">{h.matchedField}</span>
                </div>
                <div class="mt-1 font-mono text-[11px] text-black-700 dark:text-black-600 truncate">
                  {#each snippetParts(h.snippet) as p}
                    {#if p.match}
                      <span class="bg-amber-200 text-amber-900 dark:bg-amber-500/40 dark:text-amber-100 rounded px-0.5">{p.text}</span>
                    {:else}
                      <span>{p.text}</span>
                    {/if}
                  {/each}
                </div>
              </button>
            </li>
          {/each}
        {/if}
      </ul>

      <footer class="px-3 py-2 border-t border-slate-200 dark:border-navy-600 text-[10px] text-black-700 dark:text-black-500 flex items-center gap-3">
        <span>↑↓ navigate</span>
        <span>↵ open</span>
        <span class="ml-auto">Ctrl+K to toggle</span>
      </footer>
    </div>
  </div>
{/if}
