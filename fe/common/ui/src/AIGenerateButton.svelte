<script lang="ts" generics="T">
  /* ✨ Generate button over a queued generate job (see aigen.ts). Shows
     where the job is — "Queued · #2", "Writing…" — with Cancel, then the
     result as a draft with Use / Discard so a generate never overwrites
     what the user typed without asking. `autoUse` skips the draft for
     callers whose next step is already a review screen. */
  import type { Snippet } from "svelte";
  import { onDestroy } from "svelte";
  import Button from "./Button.svelte";
  import { AIGenRun, idleAIGenState, type AIGenAPI, type AIGenInput, type AIGenState } from "./aigen.js";

  type Props = {
    kind: string;
    /** Read at click time, so it sees the latest form values. */
    input: () => AIGenInput;
    /** Return a message to refuse the click (e.g. empty brief). */
    validate?: () => string;
    label?: string;
    variant?: "primary" | "secondary" | "ghost";
    size?: "sm" | "md" | "lg";
    disabled?: boolean;
    /** The value a "Use" would replace; shown above the suggestion. */
    current?: string;
    autoUse?: boolean;
    onUse: (result: T) => void;
    /** Take over error display; the button still offers Retry. */
    onError?: (message: string) => void;
    /** Custom draft preview; defaults to text / pretty JSON. */
    preview?: Snippet<[T]>;
    pollMs?: number;
    api?: AIGenAPI<T>;
    testid?: string;
  };

  let {
    kind,
    input,
    validate,
    label = "✨ Generate",
    variant = "secondary",
    size = "sm",
    disabled = false,
    current = "",
    autoUse = false,
    onUse,
    onError,
    preview,
    pollMs,
    api,
    testid = "ai-generate",
  }: Props = $props();

  let st = $state<AIGenState<T>>(idleAIGenState<T>());
  let localError = $state("");

  // svelte-ignore state_referenced_locally
  const run = new AIGenRun<T>({
    kind,
    pollMs,
    api,
    onChange: (s) => {
      st = s;
      if (s.phase === "done" && autoUse && s.result !== undefined) {
        onUse(s.result);
        run.reset();
      }
      if (s.phase === "failed" && onError) onError(s.error);
    },
  });

  onDestroy(() => run.dispose());

  function go() {
    localError = "";
    const refuse = validate?.() ?? "";
    if (refuse) {
      if (onError) onError(refuse);
      else localError = refuse;
      return;
    }
    void run.start(input());
  }

  function use() {
    if (st.result !== undefined) onUse(st.result);
    run.reset();
  }

  function asText(v: unknown): string {
    return typeof v === "string" ? v : JSON.stringify(v, null, 2);
  }

  const busy = $derived(st.phase === "queued" || st.phase === "working");
  const statusText = $derived(
    st.phase === "working" ? "Writing…" : st.position > 0 ? `Queued · #${st.position}` : "Queued…",
  );
</script>

<div class="inline-flex flex-col gap-2" data-testid={testid}>
  <div class="flex flex-wrap items-center gap-2">
    {#if busy}
      <span
        class="inline-flex items-center gap-1.5 rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-2 py-1 text-xs font-medium text-black-800 dark:text-white-100"
        role="status"
        aria-live="polite"
        data-testid="{testid}-status"
      >
        <span class="h-1.5 w-1.5 animate-pulse rounded-full {st.phase === 'working' ? 'bg-green-500' : 'bg-black-600'}"></span>
        {statusText}
      </span>
      <Button variant="ghost" size={size} onclick={() => void run.cancel()} testid="{testid}-cancel">Cancel</Button>
    {:else}
      <Button {variant} {size} disabled={disabled} onclick={go} testid="{testid}-button">
        {st.phase === "failed" ? "↻ Retry" : label}
      </Button>
    {/if}
  </div>

  {#if !onError && (localError || st.phase === "failed")}
    <p class="text-xs text-neg-400" role="alert" data-testid="{testid}-error">✗ {localError || st.error}</p>
  {/if}

  {#if st.phase === "done" && !autoUse && st.result !== undefined}
    <div
      class="rounded-lg border border-green-500 bg-white-100 dark:bg-navy-700 p-3 text-sm"
      data-testid="{testid}-draft"
    >
      {#if current.trim()}
        <p class="text-[11px] font-medium uppercase tracking-wide text-black-700 dark:text-black-600">Current</p>
        <pre class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap font-sans text-xs text-black-700 dark:text-black-600 line-through">{current}</pre>
        <p class="mt-2 text-[11px] font-medium uppercase tracking-wide text-green-600">Suggested</p>
      {:else}
        <p class="text-[11px] font-medium uppercase tracking-wide text-green-600">Draft</p>
      {/if}
      {#if preview}
        <div class="mt-1">{@render preview(st.result)}</div>
      {:else}
        <pre class="mt-1 max-h-64 overflow-auto whitespace-pre-wrap font-sans text-xs text-black-900 dark:text-white-100">{asText(st.result)}</pre>
      {/if}
      <div class="mt-3 flex items-center gap-2">
        <Button variant="primary" size="sm" onclick={use} testid="{testid}-use">Use</Button>
        <Button variant="ghost" size="sm" onclick={() => run.reset()} testid="{testid}-discard">Discard</Button>
      </div>
    </div>
  {/if}
</div>
