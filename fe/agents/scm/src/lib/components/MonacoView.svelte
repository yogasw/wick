<script lang="ts">
  // Monaco wrapper supporting two modes:
  //   - diff: show original (HEAD) vs modified (working) side by side
  //   - edit: a single editable model, surfaced via bind:value-style onChange
  // Lazy-loads monaco-editor so its large bundle is a separate chunk.
  import { onDestroy } from "svelte";

  type Props = {
    mode: "diff" | "edit";
    original?: string; // diff mode: left side
    modified: string; // diff mode: right side / edit mode: content
    language?: string;
    readOnly?: boolean;
    onChange?: (v: string) => void;
  };
  let {
    mode,
    original = "",
    modified,
    language = "plaintext",
    readOnly = false,
    onChange,
  }: Props = $props();

  let host: HTMLDivElement;
  let monaco: typeof import("monaco-editor") | null = null;
  let editor: import("monaco-editor").editor.IStandaloneCodeEditor | null = null;
  let diffEditor: import("monaco-editor").editor.IStandaloneDiffEditor | null = null;
  let ready = $state(false);

  function theme(): string {
    return document.documentElement.classList.contains("dark") ? "wick-dark" : "wick-light";
  }

  // Slim Monaco scrollbar to echo the app's global scrollbar (thin,
  // 8px). Monaco draws its own scrollbar (CSS can't reach it), so colors
  // come from the theme tokens below and size from these options.
  const scrollbarOpts = {
    verticalScrollbarSize: 8,
    horizontalScrollbarSize: 8,
    useShadows: false,
  } as const;

  // Match the global scrollbar slider colors (layout.templ): dark
  // #2c3a5a, light #cfc9b8, transparent track.
  function defineThemes(m: typeof import("monaco-editor")) {
    m.editor.defineTheme("wick-dark", {
      base: "vs-dark",
      inherit: true,
      rules: [],
      colors: {
        "scrollbar.shadow": "#00000000",
        "scrollbarSlider.background": "#2c3a5a99",
        "scrollbarSlider.hoverBackground": "#3d4f74cc",
        "scrollbarSlider.activeBackground": "#3d4f74",
      },
    });
    m.editor.defineTheme("wick-light", {
      base: "vs",
      inherit: true,
      rules: [],
      colors: {
        "scrollbar.shadow": "#00000000",
        "scrollbarSlider.background": "#cfc9b899",
        "scrollbarSlider.hoverBackground": "#b9b29dcc",
        "scrollbarSlider.activeBackground": "#b9b29d",
      },
    });
  }

  async function ensureMonaco() {
    if (monaco) return monaco;
    // The diff algorithm runs in Monaco's editor worker — without a real
    // worker the diff editor shows both sides but no add/remove colors.
    // We only need the generic editor worker (not the per-language ones),
    // loaded via Vite's `?worker` so it's bundled with a correct URL.
    const EditorWorker = (await import("monaco-editor/esm/vs/editor/editor.worker?worker")).default;
    (self as any).MonacoEnvironment = {
      getWorker: () => new EditorWorker(),
    };
    monaco = await import("monaco-editor");
    defineThemes(monaco);
    return monaco;
  }

  async function build() {
    const m = await ensureMonaco();
    if (!host) return;
    dispose();
    if (mode === "diff") {
      diffEditor = m.editor.createDiffEditor(host, {
        theme: theme(),
        readOnly: true,
        automaticLayout: true,
        renderSideBySide: true,
        fontSize: 12,
        minimap: { enabled: false },
        scrollbar: scrollbarOpts,
        // Drop the wide change-overview ruler — wasteful in a narrow panel.
        renderOverviewRuler: false,
        overviewRulerLanes: 0,
      });
      diffEditor.setModel({
        original: m.editor.createModel(original, language),
        modified: m.editor.createModel(modified, language),
      });
    } else {
      editor = m.editor.create(host, {
        value: modified,
        language,
        theme: theme(),
        readOnly,
        automaticLayout: true,
        fontSize: 12,
        minimap: { enabled: false },
        scrollbar: scrollbarOpts,
        overviewRulerLanes: 0,
        overviewRulerBorder: false,
      });
      editor.onDidChangeModelContent(() => {
        onChange?.(editor!.getValue());
      });
    }
    ready = true;
  }

  function dispose() {
    if (diffEditor) {
      const mdl = diffEditor.getModel();
      mdl?.original.dispose();
      mdl?.modified.dispose();
      diffEditor.dispose();
      diffEditor = null;
    }
    if (editor) {
      editor.getModel()?.dispose();
      editor.dispose();
      editor = null;
    }
  }

  // Rebuild ONLY when the editor's identity changes (mode/language) — NOT
  // on content change. Rebuilding on every `modified` change would tear
  // down the editor on each keystroke (onChange → modified → rebuild),
  // stealing focus + cursor. Content is synced by the separate effect
  // below via setValue, which preserves the editing session.
  $effect(() => {
    void mode;
    void language;
    if (host) build();
  });

  // Sync external content into the live editor WITHOUT rebuilding, so a
  // value change (switching diff sides, post-save reload) updates the
  // view but typing — which also flows through `modified` via onChange —
  // does not reset the cursor. setValue is a no-op when the text already
  // matches, which is the case for the user's own keystrokes.
  $effect(() => {
    const orig = original;
    const mod = modified;
    if (mode === "diff" && diffEditor) {
      const model = diffEditor.getModel();
      if (model) {
        if (model.original.getValue() !== orig) model.original.setValue(orig);
        if (model.modified.getValue() !== mod) model.modified.setValue(mod);
      }
    } else if (mode === "edit" && editor) {
      if (editor.getValue() !== mod) editor.setValue(mod);
    }
  });

  // React to dark-mode toggles.
  const obs = new MutationObserver(() => {
    if (monaco) {
      const t = theme();
      monaco.editor.setTheme(t);
    }
  });
  $effect(() => {
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => obs.disconnect();
  });

  onDestroy(dispose);
</script>

<div class="relative h-full w-full">
  <div bind:this={host} class="h-full w-full"></div>
  {#if !ready}
    <div class="absolute inset-0 flex items-center justify-center text-xs text-black-700 dark:text-black-600">
      Loading editor…
    </div>
  {/if}
</div>
