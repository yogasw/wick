<script lang="ts">
  /* Shared lazy Ace-based code editor. Ace is dynamically imported on first
     mount so it lands in its own Vite chunk — only screens that open an editor
     pay the load cost. While Ace loads (or if it fails / cannot init, e.g. under
     jsdom in tests) a plain textarea fallback keeps the field usable.

     Mode is picked from `path` (by file extension) or `language` (bare name like
     "go" / "python" / "sh"); `path` wins if both are given. Themes are prop-driven
     so each consumer keeps its own look. Sizing: omit `rows` to fill the host
     container (h-full); pass `rows` for a bordered, min/max-lines editor. */
  import { onDestroy } from "svelte";
  import { aceModeFor, aceModeForLanguage } from "./aceMode.js";

  type AceModule = typeof import("ace-builds");
  type AceEditor = import("ace-builds").Ace.Editor;

  type Theme = { light: string; dark: string };

  type Props = {
    value: string;
    onChange: (v: string) => void;
    /** File path — mode picked by extension. Takes precedence over `language`. */
    path?: string;
    /** Bare language name (e.g. "go", "python", "sh") when there's no path. */
    language?: string;
    readonly?: boolean;
    /** Ace theme names (without the "ace/theme/" prefix). */
    theme?: Theme;
    /** When set, renders a bordered editor sized by min/max lines around `rows`.
        When omitted, the editor fills its host container. */
    rows?: number;
    /** Focus the editor on mount so keyboard shortcuts (e.g. Ctrl+F for the
        in-editor find box) work immediately — useful when the editor opens in
        a modal and the user hasn't clicked into it yet. */
    autofocus?: boolean;
  };

  let {
    value,
    onChange,
    path,
    language = "text",
    readonly = false,
    theme = { light: "chrome", dark: "twilight" },
    rows,
    autofocus = false,
  }: Props = $props();

  let host: HTMLDivElement | undefined = $state();
  let editor: AceEditor | null = null;
  let ace: AceModule | null = null;
  let mounted = $state(false);
  let suppressNext = false;

  function modeFor(): string {
    return path != null ? aceModeFor(path) : aceModeForLanguage(language);
  }

  function prefersDark(): boolean {
    return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
  }

  function pickTheme(): string {
    return `ace/theme/${prefersDark() ? theme.dark : theme.light}`;
  }

  $effect(() => {
    if (!host || editor) return;
    let cancelled = false;
    let observer: MutationObserver | null = null;

    (async () => {
      try {
        const aceMod = await import("ace-builds");
        // ext-modelist covers path-based modes; explicit mode imports cover the
        // common language props. Themes: load all we might switch between.
        await import("ace-builds/src-noconflict/ext-modelist");
        await import("ace-builds/src-noconflict/mode-golang");
        await import("ace-builds/src-noconflict/mode-python");
        await import("ace-builds/src-noconflict/mode-sh");
        await import("ace-builds/src-noconflict/mode-json");
        // ext-searchbox gives Ace its own in-editor find widget so Ctrl+F /
        // Cmd+F searches within the editor instead of triggering the browser
        // page-find. Ace binds the keys automatically once this is loaded.
        await import("ace-builds/src-noconflict/ext-searchbox");
        await import("ace-builds/src-noconflict/theme-chrome");
        await import("ace-builds/src-noconflict/theme-twilight");
        await import("ace-builds/src-noconflict/theme-github");
        await import("ace-builds/src-noconflict/theme-tomorrow_night");
        if (cancelled || !host) return;
        ace = aceMod;
        const opts: Record<string, unknown> = {
          mode: modeFor(),
          theme: pickTheme(),
          fontSize: "12px",
          tabSize: 2,
          useSoftTabs: true,
          showPrintMargin: false,
          wrap: true,
          highlightActiveLine: true,
          readOnly: readonly,
        };
        if (rows != null) {
          opts.minLines = Math.max(8, Math.floor(rows / 2));
          opts.maxLines = Math.max(rows, 36);
        }
        editor = ace.edit(host, opts);
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
        if (autofocus) editor.focus();
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

  /* External value sync — mirror the caller's value in without re-emitting
     onChange, preserving cursor + scroll so a fast typist doesn't lose place.
     Read reactive deps (value, mounted) BEFORE any early return so the effect
     re-runs after the async mount AND whenever the caller swaps the value. */
  $effect(() => {
    const next = value ?? "";
    void mounted; // subscribe so this re-runs once Ace has initialized
    if (!editor) return;
    const current = editor.getValue();
    if (current === next) return;
    const cursor = editor.getCursorPosition();
    const top = editor.session.getScrollTop();
    suppressNext = true;
    editor.setValue(next, -1);
    editor.moveCursorToPosition(cursor);
    editor.session.setScrollTop(top);
    editor.clearSelection();
  });

  $effect(() => {
    const mode = modeFor();
    void mounted;
    if (!editor) return;
    editor.session.setMode(mode);
  });

  $effect(() => {
    const ro = readonly;
    void mounted;
    if (!editor) return;
    editor.setReadOnly(ro);
  });

  onDestroy(() => {
    editor?.destroy();
    editor = null;
  });
</script>

{#if rows != null}
  <!-- Bordered, row-sized editor (workflow script nodes). -->
  {#if !mounted}
    <textarea
      class="w-full rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 font-mono text-sm"
      rows={rows}
      value={value ?? ""}
      readonly={readonly}
      oninput={(e) => onChange((e.currentTarget as HTMLTextAreaElement).value)}
    ></textarea>
  {/if}
  <div
    bind:this={host}
    class="rounded border border-white-300 dark:border-navy-600 overflow-hidden"
    class:hidden={!mounted}
    style="min-height: {rows * 16}px;"
  ></div>
{:else}
  <!-- Fill-container editor (file viewer). -->
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
{/if}
