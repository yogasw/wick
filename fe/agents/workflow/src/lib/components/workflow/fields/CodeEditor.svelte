<script lang="ts">
  // CodeEditor — Ace wrapper used by the go_script / python node
  // inspectors. Mirrors v1's nodes/go_script/inspector.js behaviour
  // (syntax highlighting, dark-mode follow, cursor preservation).
  //
  // Ace is dynamically imported the first time this component
  // mounts so the main editor bundle stays slim — only operators
  // who actually open a script node pay the ~500 KB load cost. Vite
  // emits separate chunks for each `import("ace-builds/...")`.
  import { onDestroy } from "svelte";

  type AceModule = typeof import("ace-builds");
  type AceEditor = import("ace-builds").Ace.Editor;

  type Props = {
    value: string;
    language?: "go" | "python";
    onChange: (v: string) => void;
    rows?: number;
  };

  let { value, language = "go", onChange, rows = 14 }: Props = $props();

  let host: HTMLDivElement | undefined = $state();
  let editor: AceEditor | null = null;
  let ace: AceModule | null = null;
  let loading = $state(true);
  let suppressNext = false;

  function pickTheme(): string {
    return document.documentElement.classList.contains("dark")
      ? "ace/theme/tomorrow_night"
      : "ace/theme/github";
  }

  function pickMode(lang: string): string {
    return lang === "python" ? "ace/mode/python" : "ace/mode/golang";
  }

  $effect(() => {
    if (!host || editor) return;
    let cancelled = false;
    let observer: MutationObserver | null = null;

    (async () => {
      try {
        // Dynamic import + the specific modes/themes we use. Each
        // becomes its own chunk; the worker stays off (we set
        // useWorker(false) below) so we skip the worker chunks too.
        const aceMod = await import("ace-builds");
        await import("ace-builds/src-noconflict/mode-golang");
        await import("ace-builds/src-noconflict/mode-python");
        await import("ace-builds/src-noconflict/theme-github");
        await import("ace-builds/src-noconflict/theme-tomorrow_night");
        if (cancelled || !host) return;
        ace = aceMod;
        editor = ace.edit(host, {
          mode: pickMode(language),
          theme: pickTheme(),
          fontSize: "12px",
          minLines: Math.max(8, Math.floor(rows / 2)),
          maxLines: Math.max(rows, 36),
          tabSize: 2,
          useSoftTabs: true,
          showPrintMargin: false,
          wrap: true,
          highlightActiveLine: true,
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
        loading = false;
      } catch (e) {
        console.error("Ace load failed — falling back to textarea:", e);
        loading = false;
      }
    })();

    return () => {
      cancelled = true;
      observer?.disconnect();
      editor?.destroy();
      editor = null;
    };
  });

  // External value sync — mirror caller's value into the editor
  // without re-emitting onChange. Preserve cursor + scroll so a
  // fast-typing operator doesn't lose place when the parent
  // re-renders (every keystroke patches the store).
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
    editor.session.setMode(pickMode(language));
  });

  onDestroy(() => {
    editor?.destroy();
    editor = null;
  });
</script>

{#if loading}
  <!-- Plain textarea while Ace loads — keeps the modal usable even
       on slow networks and gives a sensible fallback if Ace fails. -->
  <textarea
    class="w-full rounded border border-slate-200 dark:border-navy-600 bg-white dark:bg-navy-700 px-3 py-1.5 font-mono text-sm"
    rows={rows}
    value={value ?? ""}
    oninput={(e) => onChange((e.target as HTMLTextAreaElement).value)}
  ></textarea>
{/if}
<div
  bind:this={host}
  class="rounded border border-slate-200 dark:border-navy-600 overflow-hidden"
  class:hidden={loading}
  style="min-height: {rows * 16}px;"
></div>
