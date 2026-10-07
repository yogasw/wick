<script lang="ts">
  import MarkdownBlock from "./MarkdownBlock.svelte";
  import CopyButton from "./CopyButton.svelte";
  import { parseSkill, SCOPE_LABEL } from "../skill.js";
  import type { TraceContext, TraceDisplay } from "../types.js";
  type Props = { display: TraceDisplay; ctx?: TraceContext };
  let { display }: Props = $props();

  const skill = $derived(parseSkill(display.body ?? "", display.path));
  const lines = $derived(display.lines);
</script>

<!-- A read of a SKILL.md: what the skill is first, its instructions
     below. The managed warning comment and the frontmatter are folded into
     the header — Raw still shows the file exactly as the agent read it. -->
<div data-trace-kind="skill">
  <div class="px-3 pt-2 pb-2 border-b border-white-300 dark:border-navy-600">
    <div class="flex flex-wrap items-center gap-1.5">
      <span data-skill-name class="font-mono text-[13px] font-semibold text-black-900 dark:text-white-100 break-all">{skill.name}</span>
      {#if skill.scope}
        <span data-skill-scope={skill.scope}
          class="rounded-full border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-700 px-2 py-0.5 text-[10px] font-medium text-black-800 dark:text-black-600"
        >{SCOPE_LABEL[skill.scope]}</span>
      {/if}
      {#if skill.managed}
        <span data-skill-managed title="Shipped by wick and rewritten on every start — edits here are lost"
          class="rounded-full border border-green-500 px-2 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-400"
        >Managed by wick{skill.scope === "builtin" ? " — read-only" : ""}</span>
      {/if}
      {#if lines?.partial}
        <span data-skill-partial
          class="rounded-full border border-amber-500 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300"
        >partial (lines {lines.from}–{lines.to})</span>
      {/if}
    </div>
    {#if skill.description}
      <p data-skill-description class="mt-1 text-[11px] leading-relaxed text-black-800 dark:text-black-600">{skill.description}</p>
    {/if}
    {#if display.path}
      <div class="mt-1 flex items-center gap-2">
        <span data-path class="min-w-0 flex-1 break-all font-mono text-[10px] text-black-600 dark:text-black-600">{display.path}</span>
        <CopyButton text={display.path} label="Copy path" />
      </div>
    {/if}
  </div>
  {#if skill.body.trim()}
    <MarkdownBlock display={{ kind: "markdown", lang: "markdown", body: skill.body }} />
  {:else}
    <p class="px-3 py-2 italic text-[11px] text-black-600 dark:text-black-600">no instructions in this part of the file</p>
  {/if}
</div>
