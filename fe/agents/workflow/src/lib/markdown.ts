// Markdown helpers for workflow descriptions + sticky notes. Rendering
// goes through @wick-fe/common-md, which HTML-escapes every input line
// (raw tags, <svg>, javascript: links stay inert text), so its output is
// safe for {@html}.
import { renderMarkdown } from "@wick-fe/common-md";

export { renderMarkdown };

// mdPlainText flattens markdown to one line of plain text for the
// 2-line summary on canvas cards: drops fences, heading/list/quote
// markers, emphasis and link syntax, then collapses whitespace.
export function mdPlainText(md: string): string {
  if (!md) return "";
  return md
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+|\d+[.)]\s+)/gm, "")
    .replace(/(\*\*|__|\*|_|~~|`)/g, "")
    .replace(/\s+/g, " ")
    .trim();
}
