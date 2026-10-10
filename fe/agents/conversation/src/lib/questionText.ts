import { esc, linkifyText } from "./markdown.js";

/* A teammate's question (grouped §2a) reads as text with inline code and
   links, never as markdown blocks: `x` becomes a mono <code>, URLs and
   [label](url) become links, and everything else is HTML-escaped. Code
   spans are cut out before linkifying, so a link never runs into one. */

export const QUESTION_CODE_CLASS = "font-mono text-[11.5px]";

/** The question as safe HTML: inline code and links only. */
export function questionHtml(text: string | null | undefined): string {
  return (text ?? "")
    .split(/(`[^`\n]+`)/)
    .map((part, i) =>
      i % 2 ? `<code class="${QUESTION_CODE_CLASS}">${esc(part.slice(1, -1))}</code>` : part ? linkifyText(part) : "",
    )
    .join("");
}
