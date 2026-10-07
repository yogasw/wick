/* Syntax highlighting is app-provided: common-ui does not ship
   highlight.js. An app that has a highlighter registers it once; until
   then (and in tests) CodeBlock renders plain escaped text. */
export type TraceHighlighter = (code: string, lang: string) => Promise<string | null>;

let highlighter: TraceHighlighter | null = null;

export function setTraceHighlighter(fn: TraceHighlighter | null): void {
  highlighter = fn;
}

export function getTraceHighlighter(): TraceHighlighter | null {
  return highlighter;
}
