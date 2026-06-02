<script lang="ts">
  // Interactive JSON tree. Each leaf is a draggable span carrying the
  // full Go template path so the operator can drag any value into an
  // expression-mode ArgField and have it written verbatim (n8n's
  // drag-from-INPUT pattern, ported from the legacy templ editor's
  // renderInteractiveJSON in editor.js).
  //
  // Props:
  //   value:   the JSON value to render
  //   prefix:  the template root (e.g. ".Node.parent_label") — leaves
  //            emit `{{prefix.<key>.<idx>...}}` as text/plain on dragstart
  //   draggable: when false, renders inert text (no drag handlers)

  type Props = {
    value: unknown;
    prefix?: string;
    draggable?: boolean;
  };
  let { value, prefix = "", draggable = true }: Props = $props();

  // Event-envelope root keys are rendered with their Go field casing
  // (Payload, Type, Subtype, Channel, At) so the displayed key matches
  // the path the drag would emit — operator never sees a lowercased
  // "payload" then drops "Payload" into the field.
  function canonicalEventKey(k: string, isEventRoot: boolean): string {
    if (!isEventRoot) return k;
    switch (k) {
      case "type": return "Type";
      case "subtype": return "Subtype";
      case "channel": return "Channel";
      case "at": return "At";
      case "payload": return "Payload";
      default: return k;
    }
  }

  type Leaf = {
    kind: "text" | "leaf";
    text?: string;
    path?: string;
    display?: string;
    cls?: string;
  };

  // Flatten to a token stream so the markup is one map of <span> nodes
  // instead of a recursive component (Svelte 5's $derived doesn't play
  // nicely with self-recursive components when the input mutates).
  const tokens = $derived.by<Leaf[]>(() => {
    const out: Leaf[] = [];
    const root = prefix || ".";
    walk(value, root, 0, out);
    return out;
  });

  // Build a Go-template-valid path. Numeric array indexing isn't
  // expressible with `.0` syntax — Go's text/template rejects that
  // as "unexpected .0 in operand". We wrap array steps in
  // `(index <parent> N)` so `(index .Node.foo.rows 0).name` parses
  // and evaluates to the same value the path-builder displays.
  function walk(v: unknown, path: string, depth: number, out: Leaf[]) {
    const pad = "  ".repeat(depth);
    if (v === null) {
      out.push({ kind: "leaf", path, display: "null", cls: "text-slate-400" });
      return;
    }
    if (Array.isArray(v)) {
      out.push({ kind: "text", text: "[\n" });
      v.forEach((item, i) => {
        out.push({ kind: "text", text: pad + "  " });
        // Wrap parent path in index() — `(index .Node.foo.rows 0)` —
        // so subsequent map access (`.name`) chains correctly and the
        // whole expression is a valid Go template pipeline.
        const childPath = "(index " + path + " " + i + ")";
        walk(item, childPath, depth + 1, out);
        out.push({ kind: "text", text: i < v.length - 1 ? ",\n" : "\n" });
      });
      out.push({ kind: "text", text: pad + "]" });
      return;
    }
    if (typeof v === "object") {
      const obj = v as Record<string, unknown>;
      const keys = Object.keys(obj);
      out.push({ kind: "text", text: "{\n" });
      keys.forEach((k, i) => {
        const isEventRoot = path === ".Event" && depth === 0;
        const displayKey = canonicalEventKey(k, isEventRoot);
        const childPath = path + "." + displayKey;
        out.push({ kind: "text", text: pad + "  " });
        out.push({
          kind: "leaf",
          path: childPath,
          display: '"' + displayKey + '"',
          cls: "text-sky-700 dark:text-sky-400",
        });
        out.push({ kind: "text", text: ": " });
        walk(obj[k], childPath, depth + 1, out);
        out.push({ kind: "text", text: i < keys.length - 1 ? ",\n" : "\n" });
      });
      out.push({ kind: "text", text: pad + "}" });
      return;
    }
    if (typeof v === "string") {
      out.push({ kind: "leaf", path, display: '"' + v + '"', cls: "text-emerald-700 dark:text-emerald-400" });
    } else {
      out.push({ kind: "leaf", path, display: String(v), cls: "text-amber-700 dark:text-amber-400" });
    }
  }

  function onDragStart(e: DragEvent, path: string) {
    if (!draggable) return;
    const tpl = "{{" + path + "}}";
    e.dataTransfer?.setData("text/plain", tpl);
    e.dataTransfer!.effectAllowed = "copyMove";
  }

  function onClick(e: MouseEvent, path: string) {
    const tpl = "{{" + path + "}}";
    try {
      void navigator.clipboard.writeText(tpl);
      const el = e.currentTarget as HTMLElement;
      el.classList.add("ring-2", "ring-emerald-400");
      setTimeout(() => el.classList.remove("ring-2", "ring-emerald-400"), 400);
    } catch {
      /* clipboard denied — no-op */
    }
  }
</script>

<pre class="font-mono text-[11px] text-slate-800 dark:text-slate-200 whitespace-pre-wrap leading-snug">{#each tokens as t}{#if t.kind === "text"}{t.text}{:else}<span
        class={"rounded px-0.5 cursor-grab " + (t.cls ?? "")}
        class:hover:bg-emerald-100={draggable}
        class:dark:hover:bg-emerald-900={draggable}
        draggable={draggable}
        title={draggable ? `Drag to an expression field — inserts {{${t.path}}}` : ""}
        ondragstart={(e) => onDragStart(e, t.path!)}
        onclick={(e) => onClick(e, t.path!)}
        role="button"
        tabindex="-1"
      >{t.display}</span>{/if}{/each}</pre>
