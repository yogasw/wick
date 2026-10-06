/* The kind → component table TraceBody dispatches on. A new kind (table,
   mermaid, a ticket card…) is one detector on the backend plus one
   registerTraceRenderer call here or in the app — no render site changes.
   A kind nobody registered falls back to TextBlock, never a crash. */
import type { Component } from "svelte";
import type { TraceContext, TraceDisplay } from "./types.js";
import TextBlock from "./blocks/TextBlock.svelte";
import MarkdownBlock from "./blocks/MarkdownBlock.svelte";
import JsonBlock from "./blocks/JsonBlock.svelte";
import CodeBlock from "./blocks/CodeBlock.svelte";
import DiffBlock from "./blocks/DiffBlock.svelte";
import ErrorBlock from "./blocks/ErrorBlock.svelte";
import TerminalBlock from "./blocks/TerminalBlock.svelte";
import CommandBlock from "./blocks/CommandBlock.svelte";
import FileBlock from "./blocks/FileBlock.svelte";
import SearchBlock from "./blocks/SearchBlock.svelte";
import McpBlock from "./blocks/McpBlock.svelte";
import BinaryChip from "./blocks/BinaryChip.svelte";
import SkillBlock from "./blocks/SkillBlock.svelte";

export type TraceRendererProps = { display: TraceDisplay; ctx?: TraceContext };
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type TraceRenderer = Component<TraceRendererProps, any, any>;

const renderers = new Map<string, TraceRenderer>();

export function registerTraceRenderer(kind: string, component: TraceRenderer): void {
  renderers.set(kind, component);
}

export function getTraceRenderer(kind: string): TraceRenderer {
  return renderers.get(kind) ?? TextBlock;
}

registerTraceRenderer("text", TextBlock);
registerTraceRenderer("markdown", MarkdownBlock);
registerTraceRenderer("json", JsonBlock);
registerTraceRenderer("code", CodeBlock as TraceRenderer);
registerTraceRenderer("diff", DiffBlock);
registerTraceRenderer("error", ErrorBlock);
registerTraceRenderer("terminal", TerminalBlock);
registerTraceRenderer("command", CommandBlock);
registerTraceRenderer("file", FileBlock);
registerTraceRenderer("search", SearchBlock);
registerTraceRenderer("mcp", McpBlock);
registerTraceRenderer("skill", SkillBlock);
for (const k of ["image", "pdf", "audio", "video", "binary"]) registerTraceRenderer(k, BinaryChip);
