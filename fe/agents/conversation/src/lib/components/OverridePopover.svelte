<script lang="ts">
  /* The `/`-menu session-override popover (opened by /thinking). Floats above
     the composer like SwitchModal. It renders the provider's per-session
     override schema with the generic ConfigForm and SAVES each edit through the
     wick overrides endpoint — it never sends a chat message. State is
     runtime-only; a fresh session shows the configured baseline. Esc or a click
     outside closes. */
  import { ConfigForm, type ConfigField } from "@wick-fe/common-ui";

  type Props = {
    open: boolean;
    title: string;
    schema: ConfigField[];
    values: Record<string, string>;
    /** Persist one field. Parent calls the endpoint + refreshes `values`. */
    onChange: (key: string, value: string) => void;
    onClose: () => void;
  };

  let { open, title, schema, values, onChange, onClose }: Props = $props();

  let el: HTMLDivElement | undefined = $state();

  $effect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); onClose(); }
    }
    function onDown(e: MouseEvent) {
      if (el && !el.contains(e.target as Node)) onClose();
    }
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("mousedown", onDown, true);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("mousedown", onDown, true);
    };
  });
</script>

{#if open}
  <div
    bind:this={el}
    data-override-popup
    class="absolute bottom-full left-0 right-0 mb-2 z-30 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg overflow-hidden"
  >
    <div class="px-3 py-2 border-b border-white-300 dark:border-navy-600 text-xs font-semibold text-black-900 dark:text-white-100">
      {title}
    </div>
    <div class="p-3">
      {#if schema.length === 0}
        <p class="text-xs text-black-600 dark:text-black-700">No session settings for this provider.</p>
      {:else}
        <ConfigForm {schema} {values} {onChange} />
        <p class="mt-3 text-[11px] text-black-600 dark:text-black-700">
          Applies to this session only — saved as you change it, no message sent.
        </p>
      {/if}
    </div>
  </div>
{/if}
