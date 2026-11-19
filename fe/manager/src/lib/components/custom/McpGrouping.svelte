<script lang="ts">
  /* Grouping recipe editor for an MCP connector: the JSON recipe, a
     Suggest button (guess from the tools), and a live preview of the
     groups the tools land in — with the catch-all "Other" bucket and pins
     that point at tools the server no longer lists. The parent owns the
     recipe string and saves it with the form (form.group_rules). */
  import { previewMcpGrouping } from "$lib/api.js";
  import type { McpGroupPreview, McpTool } from "$lib/types.js";

  type Props = {
    tools: McpTool[];
    rules: string;
    onChange: (rules: string) => void;
  };
  let { tools, rules, onChange }: Props = $props();

  let preview = $state<McpGroupPreview | null>(null);
  let error = $state("");
  let busy = $state(false);
  let byTool = $derived(new Map((preview?.placements ?? []).map((p) => [p.tool, p.by])));
  let otherTitle = $derived.by(() => {
    try {
      return (JSON.parse(rules || "{}").other as string) || "Other";
    } catch {
      return "Other";
    }
  });

  async function run(suggest: boolean) {
    error = "";
    busy = true;
    try {
      const res = await previewMcpGrouping(tools, rules, suggest);
      if (suggest) onChange(res.rules);
      preview = res.preview;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      preview = null;
    } finally {
      busy = false;
    }
  }

  let ranOnce = false;
  $effect(() => {
    if (!ranOnce && (tools ?? []).length > 0 && (rules ?? "").trim() !== "") {
      ranOnce = true;
      void run(false);
    }
  });
</script>

<div class="space-y-3" data-cc-grouping>
  <div class="flex flex-wrap items-center gap-2">
    <button
      type="button"
      class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-900 dark:text-white-100 hover:border-green-400 disabled:opacity-50"
      disabled={busy || (tools ?? []).length === 0}
      onclick={() => run(true)}
      data-cc-grouping-suggest>Suggest from tools</button>
    <button
      type="button"
      class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-900 dark:text-white-100 hover:border-green-400 disabled:opacity-50"
      disabled={busy || (tools ?? []).length === 0}
      onclick={() => run(false)}
      data-cc-grouping-preview>Preview</button>
    {#if (rules ?? "").trim() !== ""}
      <button
        type="button"
        class="rounded-lg px-2 py-1.5 text-xs text-black-700 dark:text-black-600 hover:text-neg-400"
        onclick={() => { onChange(""); preview = null; }}
        data-cc-grouping-clear>Clear</button>
    {/if}
    {#if (tools ?? []).length === 0}
      <span class="text-[11px] text-black-700 dark:text-black-600">Run Test to load the tools first.</span>
    {/if}
  </div>

  <textarea
    class="h-40 w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-2 font-mono text-[11px] text-black-900 dark:text-white-100"
    placeholder={'{"strip_suffix": "-tool", "templates": [{"from": "description", "pattern": "Requires {group}\\\\."}], "other": "Other"}'}
    value={rules ?? ""}
    oninput={(e) => onChange((e.currentTarget as HTMLTextAreaElement).value)}
    data-cc-grouping-rules
  ></textarea>
  <p class="text-[11px] leading-relaxed text-black-700 dark:text-black-600">
    First match wins: the server's own category → <code class="font-mono">pins</code> (exact tool name) →
    <code class="font-mono">rules</code> (regex → group) → <code class="font-mono">templates</code> (regex with
    <code class="font-mono">{"{group}"}</code>; a new resource gets its own group) → <code class="font-mono">other</code>.
    Re-applied on every sync, so new tools land in a group without an edit.
  </p>

  {#if error}
    <p class="text-xs text-neg-400" data-cc-grouping-error>{error}</p>
  {/if}

  {#if preview}
    <div class="space-y-2" data-cc-grouping-result>
      <p class="text-xs text-black-800 dark:text-black-600">
        {preview.total} tools · {preview.groups.length} groups ·
        <span class={(preview.other ?? []).length > 0 ? "text-warn-400 font-semibold" : ""}>{(preview.other ?? []).length} in {otherTitle}</span>
      </p>
      {#each preview.groups as g (g.title)}
        <div
          class="rounded-lg border px-3 py-2 {g.title === otherTitle
            ? 'border-warn-400 bg-white-100 dark:bg-navy-700'
            : 'border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700'}"
          data-cc-grouping-group
        >
          <p class="text-xs font-semibold text-black-900 dark:text-white-100">{g.title} <span class="font-normal text-black-700 dark:text-black-600">({g.tools.length})</span></p>
          <p class="mt-1 flex flex-wrap gap-1">
            {#each g.tools as t (t)}
              <span class="rounded bg-white-200 dark:bg-navy-800 px-1.5 py-0.5 font-mono text-[10px] text-black-800 dark:text-black-600" title={"placed by " + (byTool.get(t) ?? "")}>{t}</span>
            {/each}
          </p>
        </div>
      {/each}
      {#if (preview.orphan_pins ?? []).length > 0}
        <p class="text-[11px] text-warn-400" data-cc-grouping-orphans>Pins for tools the server no longer lists: {(preview.orphan_pins ?? []).join(", ")}</p>
      {/if}
    </div>
  {/if}
</div>
