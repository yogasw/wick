import { esc, linkifyText } from "./markdown.js";

/* A teammate's question (grouped §2a) reads as text with inline code and
   links, never as markdown blocks: `x` becomes a mono <code>, URLs and
   [label](url) become links, and everything else is HTML-escaped. Code
   spans are cut out before linkifying, so a link never runs into one. */

export const QUESTION_CODE_CLASS = "font-mono text-[11.5px]";

/* A task thread's messages draw inline code as a chip (grouped thread
   `.msg .mt code`); a needs-input question stays plain mono. */
export const THREAD_CODE_CLASS =
  "rounded-sm border border-white-300 bg-white-100 px-[3px] font-mono text-[11px] dark:border-navy-600 dark:bg-navy-800";

/** The question as safe HTML: inline code and links only. `codeClass`
    styles the <code> spans (plain mono by default). */
export function questionHtml(text: string | null | undefined, codeClass: string = QUESTION_CODE_CLASS): string {
  return (text ?? "")
    .split(/(`[^`\n]+`)/)
    .map((part, i) =>
      i % 2 ? `<code class="${codeClass}">${esc(part.slice(1, -1))}</code>` : part ? linkifyText(part) : "",
    )
    .join("");
}
