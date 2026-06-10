<script lang="ts">
  // Monaco wrapper supporting two modes:
  //   - diff: show original (HEAD) vs modified (working) side by side
  //   - edit: a single editable model, surfaced via bind:value-style onChange
  // Lazy-loads monaco-editor so its large bundle is a separate chunk.
  import { onDestroy } from "svelte";

  type Props = {
    mode: "diff" | "edit";
    original?: string;
    modified: string;
    language?: string;
    readOnly?: boolean;
    sideBySide?: boolean;
    onChange?: (v: string) => void;
    onDirty?: (value: string) => void; // diff mode: fires when user edits modified side
  };
  let {
    mode,
    original = "",
    modified,
    language = "plaintext",
    readOnly = false,
    sideBySide = false,
    onChange,
    onDirty,
  }: Props = $props();

  let host: HTMLDivElement;
  let monaco: typeof import("monaco-editor") | null = null;
  let editor: import("monaco-editor").editor.IStandaloneCodeEditor | null = null;
  let diffEditor: import("monaco-editor").editor.IStandaloneDiffEditor | null = null;
  let ready = $state(false);

  function theme(): string {
    return document.documentElement.classList.contains("dark") ? "wick-dark" : "wick-light";
  }

  const scrollbarOpts = {
    verticalScrollbarSize: 8,
    horizontalScrollbarSize: 8,
    useShadows: false,
  } as const;

  let containerWidth = $state(0);

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
        "editorGutter.background": "#14263a",
        "editor.background": "#14263a",
        "diffEditor.insertedLineBackground": "#1a3a2a40",
        "diffEditor.removedLineBackground": "#3a1a1a40",
        "diffEditor.insertedTextBorder": "#00000000",
        "diffEditor.removedTextBorder": "#00000000",
        "focusBorder": "#00000000",
        "contrastBorder": "#00000000",
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
        "editorGutter.background": "#ffffff",
        "editor.background": "#ffffff",
        "diffEditor.insertedLineBackground": "#d4edda40",
        "diffEditor.removedLineBackground": "#f8d7da40",
        "diffEditor.insertedTextBorder": "#00000000",
        "diffEditor.removedTextBorder": "#00000000",
        "focusBorder": "#00000000",
        "contrastBorder": "#00000000",
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

  const sharedEditorOpts = {
    fontSize: 12,
    fontFamily: "'JetBrains Mono', 'Cascadia Code', 'Fira Code', Consolas, monospace",
    lineHeight: 18,
    minimap: { enabled: true },
    scrollbar: scrollbarOpts,
    renderOverviewRuler: false,
    overviewRulerLanes: 0,
    overviewRulerBorder: false,
    folding: true,
    showFoldingControls: "always" as const,
    lineNumbers: "on" as const,
    lineNumbersMinChars: 3,
    lineDecorationsWidth: 0,
    glyphMargin: false,
    renderLineHighlight: "all" as const,
    smoothScrolling: true,
    cursorBlinking: "smooth" as const,
  } as const;

  // Options applied to each inner editor of the diff editor (original + modified).
  // createDiffEditor opts don't propagate fully to inner editors.
  const innerEditorOpts = {
    fontSize: 12,
    fontFamily: "'JetBrains Mono', 'Cascadia Code', 'Fira Code', Consolas, monospace",
    lineHeight: 18,
    minimap: { enabled: false },
    lineNumbers: "on" as const,
    lineNumbersMinChars: 3,
    lineDecorationsWidth: 0,
    glyphMargin: false,
    renderLineHighlight: "all" as const,
    folding: true,
    showFoldingControls: "always" as const,
  } as const;

  async function build() {
    const m = await ensureMonaco();
    if (!host) return;
    dispose();
    if (mode === "diff") {
      diffEditor = m.editor.createDiffEditor(host, {
        theme: theme(),
        readOnly: false,
        automaticLayout: true,
        renderSideBySide: sideBySide,
        hideUnchangedRegions: { enabled: true, minimumLineCount: 3, contextLineCount: 3 },
        renderGutterMenu: true,
        ignoreTrimWhitespace: false,
        ...sharedEditorOpts,
      });
      const modModel = m.editor.createModel(modified, language);
      modModel.onDidChangeContent(() => {
        onDirty?.(modModel.getValue());
      });
      diffEditor.setModel({
        original: m.editor.createModel(original, language),
        modified: modModel,
      });
      diffEditor.getOriginalEditor().updateOptions(innerEditorOpts);
      diffEditor.getModifiedEditor().updateOptions(innerEditorOpts);
    } else {
      editor = m.editor.create(host, {
        value: modified,
        language,
        theme: theme(),
        readOnly,
        automaticLayout: true,
        ...sharedEditorOpts,
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

  // Rebuild when identity changes (mode/language/sideBySide) — NOT on content.
  $effect(() => {
    void mode;
    void language;
    void sideBySide;
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

  function resizeWatch(el: HTMLElement) {
    const ro = new ResizeObserver((entries) => {
      containerWidth = entries[0]?.contentRect.width ?? el.offsetWidth;
    });
    ro.observe(el);
    containerWidth = el.offsetWidth;
    return { destroy: () => ro.disconnect() };
  }
</script>

<div
  class="relative h-full w-full"
  use:resizeWatch
>
  <div bind:this={host} class="h-full w-full"></div>
  {#if !ready}
    <div class="absolute inset-0 flex items-center justify-center text-xs text-black-700 dark:text-black-600">
      Loading editor…
    </div>
  {/if}
</div>
