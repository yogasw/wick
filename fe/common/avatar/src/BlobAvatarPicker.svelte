<script lang="ts">
  /* Picker for a blob-mascot avatar: shape grid, expression grid, palette
     swatches plus a free color, and a shuffle. Shared by Settings → Avatar
     and the new-agent wizard. Every preview is a still frame (cached PNG),
     so the 24 tiles cost one draw each and no animation loop. */
  import BlobAvatar from "./BlobAvatar.svelte";
  import { BLOB_COLORS, BLOB_EXPRESSIONS, BLOB_SHAPES, blobColor, normalizeBlobExpression, normalizeBlobShape, randomBlob, type BlobLook } from "./blob.js";

  type Props = {
    shape?: string;
    expression?: string;
    color?: string;
    onChange: (look: BlobLook) => void;
    /** Smaller tiles and no captions (the wizard). */
    compact?: boolean;
    labelClass?: string;
  };
  let { shape, expression, color, onChange, compact = false, labelClass = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600" }: Props = $props();

  const look = $derived<BlobLook>({ shape: normalizeBlobShape(shape), expression: normalizeBlobExpression(expression), color: blobColor(color) });
  const tile = $derived(compact ? 28 : 40);
  const set = (patch: Partial<BlobLook>) => onChange({ ...look, ...patch });
  const tileClass = (on: boolean) =>
    `flex flex-col items-center rounded-xl border-2 p-1 ${on ? "border-green-500" : "border-white-300 dark:border-navy-600"}`;
</script>

<div class="space-y-3" data-testid="blob-picker">
  <div>
    <div class="flex items-center justify-between">
      <span class={labelClass}>Shape</span>
      <button
        type="button"
        class="rounded-lg border border-white-300 px-2 py-0.5 text-xs text-black-800 hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
        data-testid="blob-shuffle"
        onclick={() => onChange(randomBlob())}
      >Shuffle</button>
    </div>
    <div class="grid grid-cols-6 gap-1.5">
      {#each BLOB_SHAPES as s (s)}
        <button type="button" class={tileClass(look.shape === s)} aria-label={s} title={s} aria-pressed={look.shape === s} onclick={() => set({ shape: s })}>
          <BlobAvatar shape={s} expression={look.expression} color={look.color} size={tile} />
        </button>
      {/each}
    </div>
  </div>
  <div>
    <span class={labelClass}>Expression</span>
    <div class="grid grid-cols-6 gap-1.5">
      {#each BLOB_EXPRESSIONS as e (e)}
        <button type="button" class={tileClass(look.expression === e)} aria-label={e} title={e} aria-pressed={look.expression === e} onclick={() => set({ expression: e })}>
          <BlobAvatar shape={look.shape} expression={e} color={look.color} size={tile} />
          {#if !compact}<span class="mt-0.5 max-w-full truncate text-[10px] text-black-800 dark:text-black-600">{e}</span>{/if}
        </button>
      {/each}
    </div>
  </div>
  <div>
    <span class={labelClass}>Color</span>
    <div class="flex flex-wrap items-center gap-1.5">
      {#each BLOB_COLORS as col (col)}
        <button
          type="button"
          class="{compact ? 'h-6 w-6' : 'h-8 w-8'} rounded-full border-2 {look.color === col ? 'border-green-500' : 'border-white-300 dark:border-navy-600'}"
          style:background-color={col}
          aria-label={col}
          aria-pressed={look.color === col}
          onclick={() => set({ color: col })}
        ></button>
      {/each}
      <input
        type="color"
        class="{compact ? 'h-6 w-8' : 'h-8 w-10'} cursor-pointer rounded-lg border border-white-300 dark:border-navy-600 bg-transparent p-0.5"
        aria-label="Other color"
        title="Other color"
        value={look.color.length === 7 ? look.color : "#4b8fea"}
        oninput={(e) => set({ color: e.currentTarget.value })}
      />
    </div>
  </div>
</div>
