<script lang="ts">
  /* Lazy Ace-based syntax-highlighted editor for the file viewer.
     Ace is dynamically imported on first mount so it lands in its own
     Vite chunk — only operators who open a code file pay the load cost.
     While Ace loads (or if it fails / cannot init, e.g. under jsdom in
     tests), a plain textarea fallback keeps the file editable.

     NOTE (dedup): fe/agents/workflow ships a near-identical Ace wrapper
     (workflow/fields/CodeEditor.svelte). A shared wrapper in
     @wick-fe/common-ui is the right long-term home — extract when a
     third consumer appears, per the fe-module dedup rule. */
  import { onDestroy } from "svelte";
  import { aceModeFor } from "../aceMode.js";

  type AceModule = typeof import("ace-builds");
  type AceEditor = import("ace-builds").Ace.Editor;

  type Props = {
    path: string;
    value: string;
    onChange: (v: string) => void;
    readonly?: boolean;
  };

  let { path, value, onChange, readonly = false }: Props = $props();

  let host: HTMLDivElement | undefined = $state();
  let editor: AceEditor | null = null;
  let ace: AceModule | null = null;
  let mounted = $state(false);
  let suppressNext = false;

  function prefersDark(): boolean {
    return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
  }

  function pickTheme(): string {
    return prefersDark() ? "ace/theme/twilight" : "ace/theme/chrome";
  }

  $effect(() => {
    if (!host || editor) return;
    let cancelled = false;
    let observer: MutationObserver | null = null;

    (async () => {
      try {
        const aceMod = await import("ace-builds");
        await import("ace-builds/src-noconflict/ext-modelist");
        await import("ace-builds/src-noconflict/theme-chrome");
        await import("ace-builds/src-noconflict/theme-twilight");
        if (cancelled || !host) return;
        ace = aceMod;
        editor = ace.edit(host, {
          mode: aceModeFor(path),
          theme: pickTheme(),
          fontSize: "12px",
          tabSize: 2,
          useSoftTabs: true,
          showPrintMargin: false,
          wrap: true,
          highlightActiveLine: true,
          readOnly: readonly,
        });
        editor.session.setUseWorker(false);
        editor.setValue(value ?? "", -1);
        editor.on("change", () => {
          if (suppressNext) {
            suppressNext = false;
            return;
          }
          onChange(editor!.getValue());
        });
        observer = new MutationObserver(() => editor?.setTheme(pickTheme()));
        observer.observe(document.documentElement, {
          attributes: true,
          attributeFilter: ["class"],
        });
        mounted = true;
      } catch (e) {
        console.error("Ace load failed — falling back to textarea:", e);
        mounted = false;
      }
    })();

    return () => {
      cancelled = true;
      observer?.disconnect();
      editor?.destroy();
      editor = null;
      mounted = false;
    };
  });

  $effect(() => {
    if (!editor) return;
    const current = editor.getValue();
    if (current === value) return;
    const cursor = editor.getCursorPosition();
    const top = editor.session.getScrollTop();
    suppressNext = true;
    editor.setValue(value ?? "", -1);
    editor.moveCursorToPosition(cursor);
    editor.session.setScrollTop(top);
    editor.clearSelection();
  });

  $effect(() => {
    if (!editor) return;
    editor.session.setMode(aceModeFor(path));
  });

  $effect(() => {
    if (!editor) return;
    editor.setReadOnly(readonly);
  });

  onDestroy(() => {
    editor?.destroy();
    editor = null;
  });
</script>

<div data-testid="code-editor" class="relative w-full h-full">
  {#if !mounted}
    <textarea
      class="w-full h-full p-4 text-xs font-mono text-black-900 dark:text-white-100 bg-white-100 dark:bg-navy-800 resize-none focus:outline-none"
      value={value ?? ""}
      readonly={readonly}
      oninput={(e) => onChange((e.currentTarget as HTMLTextAreaElement).value)}
    ></textarea>
  {/if}
  <div bind:this={host} class="w-full h-full" class:hidden={!mounted}></div>
</div>
