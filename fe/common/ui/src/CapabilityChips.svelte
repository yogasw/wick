<script lang="ts">
  /* Capability display for a model row, in one of three modes:
       - "list" (default): one compact row per capability — icon + name + value.
         Clearest at a glance; no ambiguity about which icon means what.
       - "name": inline text chips (vision, reasoning, 500K ctx).
       - "icon": inline icon + tooltip (most compact).
     Shows only the `inline` keys; the rest fold behind an ⓘ that opens the
     full detail modal via onInfo. Renders nothing when the model declares no
     capabilities. The key→label/icon mapping lives in capability-types.ts. */
  import type { ModelCaps, CapabilityDisplayMode } from "./capability-types.js";
  import { capDescriptor, fmtTokens, hasAnyCaps } from "./capability-types.js";

  type Props = {
    caps: ModelCaps | undefined;
    mode?: CapabilityDisplayMode;
    /** Which capability keys to show before the ⓘ. */
    inline?: string[];
    /** Opens the detail modal. When omitted the ⓘ affordance is hidden. */
    onInfo?: () => void;
  };
  let {
    caps,
    mode = "list",
    inline = ["contextWindow", "maxOutput", "vision", "reasoning"],
    onInfo,
  }: Props = $props();

  // An inline entry renders only when the model HAS that capability: a present
  // number, or a truthy boolean. `value` is what "list"/"name" show alongside.
  type Entry = { key: string; label: string; title: string; icon: string; value: string; bool: boolean };
  const entries = $derived.by<Entry[]>(() => {
    if (!caps) return [];
    const out: Entry[] = [];
    for (const key of inline) {
      const d = capDescriptor(key);
      const v = caps[key];
      if (!d) continue;
      if (d.kind === "number" && typeof v === "number" && v > 0) {
        out.push({ key, label: d.label === "context" ? "context" : d.label, title: `${d.explain} (${v.toLocaleString()} tokens)`, icon: d.icon, value: fmtTokens(v), bool: false });
      } else if (d.kind === "bool" && v === true) {
        out.push({ key, label: d.label, title: d.explain, icon: d.icon, value: "yes", bool: true });
      }
    }
    return out;
  });
  const showInfo = $derived(!!onInfo && hasAnyCaps(caps));
</script>

{#if entries.length > 0 || showInfo}
  {#if mode === "list"}
    <!-- One row per capability: icon + name, value right-aligned. -->
    <span class="mt-1 flex flex-col gap-0.5">
      {#each entries as e (e.key)}
        <span class="flex items-center gap-1.5 text-[11px] text-black-800 dark:text-black-500" title={e.title}>
          <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d={e.icon} /></svg>
          <span class="flex-1">{e.label}</span>
          <span class="font-medium {e.bool ? 'text-green-600 dark:text-green-400' : 'text-black-900 dark:text-white-100'}">{e.value}</span>
        </span>
      {/each}
      {#if showInfo}
        <button
          type="button"
          onclick={(ev) => { ev.stopPropagation(); onInfo?.(); }}
          class="mt-0.5 inline-flex w-fit items-center gap-1 text-[10px] font-medium text-green-600 dark:text-green-400 hover:underline"
        >
          <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.4"><circle cx="8" cy="8" r="6.5"/><path d="M8 7.2v3.3" stroke-linecap="round"/><circle cx="8" cy="5.2" r="0.6" fill="currentColor" stroke="none"/></svg>
          all capabilities
        </button>
      {/if}
    </span>
  {:else}
    <!-- Inline chips: text ("name") or icon+tooltip ("icon"), + ⓘ. -->
    <span class="mt-0.5 flex flex-wrap items-center gap-1">
      {#each entries as e (e.key)}
        {#if mode === "icon"}
          <span title={`${e.label}: ${e.value}`} class="inline-flex items-center gap-0.5 rounded bg-white-300 dark:bg-navy-600 px-1 py-0.5 text-[10px] font-medium text-black-800 dark:text-black-500">
            <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d={e.icon} /></svg>
            {#if !e.bool}<span>{e.value}</span>{/if}
          </span>
        {:else}
          <span title={e.title} class="rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[10px] font-medium text-black-800 dark:text-black-500">{e.bool ? e.label : `${e.value} ${e.label === "context" ? "ctx" : e.label === "max output" ? "out" : e.label}`}</span>
        {/if}
      {/each}
      {#if showInfo}
        <button
          type="button"
          onclick={(ev) => { ev.stopPropagation(); onInfo?.(); }}
          title="Show all capabilities"
          aria-label="Show all capabilities"
          class="inline-flex h-4 w-4 items-center justify-center rounded-full text-green-600 dark:text-green-400 hover:bg-green-500/10 transition-colors"
        >
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.4"><circle cx="8" cy="8" r="6.5"/><path d="M8 7.2v3.3" stroke-linecap="round"/><circle cx="8" cy="5.2" r="0.6" fill="currentColor" stroke="none"/></svg>
        </button>
      {/if}
    </span>
  {/if}
{/if}
