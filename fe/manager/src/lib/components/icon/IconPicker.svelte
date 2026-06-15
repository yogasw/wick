<script lang="ts">
  /* Connector/job/tool icon control, ported from icon_picker.templ +
     icon_picker.js. A preview button opens a panel with an emoji grid plus
     a custom paste slot for inline <svg> / data:image base64 (capped at
     32KB client-side; the server re-validates). The emoji grid is loaded
     lazily via dynamic import("./emojiPicker.js") only on first open, so the
     ~400KB vendor lib + data never enter the main bundle and unit tests that
     never open the grid never touch the heavy import. */
  import { onDestroy } from "svelte";
  import { Button } from "@wick-fe/common-ui";

  type Props = {
    value: string;
    onChange: (value: string) => void;
    ariaLabel?: string;
  };
  let { value, onChange, ariaLabel = "Pick an icon" }: Props = $props();

  const MAX_BYTES = 32 * 1024;

  let open = $state(false);
  let custom = $state("");
  let errorMsg = $state("");
  let emojiError = $state("");
  let mountEl = $state<HTMLElement | null>(null);
  let rootEl = $state<HTMLElement | null>(null);
  let pickerEl: HTMLElement | null = null;
  let mounted = false;

  type IconKind = "empty" | "image" | "text";
  let kind = $derived<IconKind>(previewKind(value));

  function previewKind(v: string): IconKind {
    const t = (v ?? "").trim();
    if (!t) return "empty";
    if (t.startsWith("data:image/") || t.startsWith("<svg")) return "image";
    return "text";
  }

  function imageSrc(v: string): string {
    const t = v.trim();
    if (t.startsWith("<svg")) {
      return `data:image/svg+xml;base64,${btoa(unescape(encodeURIComponent(t)))}`;
    }
    return t;
  }

  function setValue(v: string): void {
    onChange(v);
    open = false;
  }

  function toggle(): void {
    errorMsg = "";
    custom = "";
    open = !open;
    if (open) mountPicker();
  }

  async function mountPicker(): Promise<void> {
    if (mounted || !mountEl) return;
    mounted = true;
    emojiError = "";
    try {
      const mod = await import("./emojiPicker.js");
      if (!mountEl) return;
      pickerEl = await mod.mountEmojiPicker(mountEl, (native) => setValue(native));
    } catch (e) {
      mounted = false;
      emojiError = e instanceof Error ? e.message : "Emoji picker unavailable.";
    }
  }

  function applyCustom(): void {
    errorMsg = "";
    const v = custom.trim();
    if (!v) return;
    if (new Blob([v]).size > MAX_BYTES) {
      errorMsg = "Too large — max 32KB.";
      return;
    }
    if (v.startsWith("data:") && !v.startsWith("data:image/")) {
      errorMsg = "Only data:image/… payloads are allowed.";
      return;
    }
    if (v.startsWith("<") && !v.startsWith("<svg")) {
      errorMsg = "Only inline <svg> markup is allowed.";
      return;
    }
    setValue(v);
  }

  function onWindowClick(e: MouseEvent): void {
    if (!open || !rootEl) return;
    if (!e.composedPath().some((n) => n === rootEl)) open = false;
  }

  $effect(() => {
    window.addEventListener("click", onWindowClick);
    return () => window.removeEventListener("click", onWindowClick);
  });

  onDestroy(() => {
    if (pickerEl && pickerEl.parentNode) pickerEl.parentNode.removeChild(pickerEl);
    pickerEl = null;
  });
</script>

<div bind:this={rootEl} class="relative inline-block">
  <button
    type="button"
    onclick={toggle}
    title={ariaLabel}
    aria-label={ariaLabel}
    aria-expanded={open}
    class="flex h-10 w-14 items-center justify-center rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 text-xl hover:border-green-400"
  >
    <span class="flex h-7 w-7 items-center justify-center overflow-hidden {kind === 'empty' ? 'opacity-40' : ''}">
      {#if kind === "image"}
        <img src={imageSrc(value)} class="h-7 w-7 object-contain" alt="" />
      {:else if kind === "text"}
        {value}
      {:else}
        🔌
      {/if}
    </span>
  </button>

  {#if open}
    <div
      class="absolute left-0 z-30 mt-2 w-max rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-3 shadow-lg"
    >
      <div bind:this={mountEl}></div>
      {#if emojiError}
        <p class="text-[11px] text-neg-400">{emojiError}</p>
      {/if}
      <label for="ip-custom" class="mt-3 block text-[11px] font-medium text-black-800 dark:text-black-600">
        Custom — any emoji, inline SVG, or data:image…;base64 (max 32KB)
      </label>
      <textarea
        id="ip-custom"
        rows="2"
        bind:value={custom}
        placeholder="<svg …>…</svg> or data:image/png;base64,…"
        class="mt-1 w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1.5 font-mono text-[11px] text-black-900 dark:text-white-100 outline-none focus:border-green-500"
      ></textarea>
      {#if errorMsg}
        <p class="mt-1 text-[11px] text-neg-400">{errorMsg}</p>
      {/if}
      <div class="mt-2 flex justify-end">
        <Button size="sm" onclick={applyCustom}>Apply</Button>
      </div>
    </div>
  {/if}
</div>
