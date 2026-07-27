<script lang="ts">
  /* Full capability breakdown for one model. Lists EVERY key the vendor
     reported — known capabilities (with a human explanation + a badge marking
     whether wick's engine acts on it yet) first, then any unknown keys
     verbatim, so a capability wick doesn't handle today is still visible for
     future work. Built on the shared Modal shell. */
  import Modal from "./Modal.svelte";
  import type { ModelCaps } from "./capability-types.js";
  import { CAP_DESCRIPTORS, capDescriptor, fmtTokens } from "./capability-types.js";

  type Props = {
    open: boolean;
    onClose: () => void;
    caps: ModelCaps | undefined;
    modelId: string;
  };
  let { open, onClose, caps, modelId }: Props = $props();

  type Row = { key: string; label: string; explain: string; handled: boolean | null; display: string; on: boolean };

  // Render known descriptors in their defined order (only those the vendor
  // actually sent), then any extra keys the vendor included that we don't have
  // a descriptor for — shown generically so nothing is hidden.
  const rows = $derived.by<Row[]>(() => {
    if (!caps) return [];
    const out: Row[] = [];
    const seen = new Set<string>();
    const render = (v: unknown): { display: string; on: boolean } => {
      if (typeof v === "boolean") return { display: v ? "yes" : "no", on: v };
      if (typeof v === "number") return { display: v.toLocaleString(), on: v > 0 };
      if (typeof v === "string") return { display: v || "—", on: v.trim() !== "" };
      if (v == null) return { display: "—", on: false };
      return { display: JSON.stringify(v), on: true };
    };
    for (const d of CAP_DESCRIPTORS) {
      if (!(d.key in caps)) continue;
      seen.add(d.key);
      const raw = caps[d.key];
      const r = d.kind === "number" && typeof raw === "number" ? { display: fmtTokens(raw) + ` (${raw.toLocaleString()})`, on: raw > 0 } : render(raw);
      out.push({ key: d.key, label: d.label, explain: d.explain, handled: d.handled, display: r.display, on: r.on });
    }
    for (const [k, v] of Object.entries(caps)) {
      if (seen.has(k)) continue;
      const r = render(v);
      out.push({ key: k, label: k, explain: "Reported by the provider; not a capability wick recognizes.", handled: null, display: r.display, on: r.on });
    }
    return out;
  });
</script>

<Modal {open} {onClose} title={`Capabilities · ${modelId}`} size="md">
  {#if rows.length === 0}
    <p class="px-1 py-2 text-sm text-black-700 dark:text-black-600">This provider didn't report any capabilities for this model.</p>
  {:else}
    <p class="mb-3 text-[11px] text-black-700 dark:text-black-600">
      Reported by the provider. <span class="rounded bg-green-500/10 px-1 py-0.5 font-medium text-green-600 dark:text-green-400">used</span> = wick's engine acts on it today;
      <span class="rounded bg-white-300 dark:bg-navy-600 px-1 py-0.5 font-medium text-black-700 dark:text-black-500">not yet</span> = declared but not wired in wick.
    </p>
    <div class="divide-y divide-white-300 dark:divide-navy-600">
      {#each rows as r (r.key)}
        <div class="flex items-start justify-between gap-3 py-2">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="font-mono text-xs font-semibold text-black-900 dark:text-white-100">{r.label}</span>
              {#if r.handled === true}
                <span class="rounded bg-green-500/10 px-1 py-0.5 text-[9px] font-medium uppercase tracking-wide text-green-600 dark:text-green-400">used</span>
              {:else if r.handled === false}
                <span class="rounded bg-white-300 dark:bg-navy-600 px-1 py-0.5 text-[9px] font-medium uppercase tracking-wide text-black-700 dark:text-black-500">not yet</span>
              {/if}
            </div>
            <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600 leading-snug">{r.explain}</p>
          </div>
          <span class="shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium {r.on ? 'bg-green-500/10 text-green-600 dark:text-green-400' : 'bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-500'}">{r.display}</span>
        </div>
      {/each}
    </div>
  {/if}
</Modal>
