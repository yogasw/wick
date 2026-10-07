/* A trace note is the agent's own words between tool calls — streamed as
   either `thinking` or `text` depending on the provider and model, and meant
   to read the same either way.

   Streaming leaves whitespace at the seams: a segment often starts or ends on
   "\n\n", and a fragment can be nothing but newlines. Under whitespace-pre-wrap
   that renders as empty rows at the top or bottom of the card, or as a card
   with no text at all. tidyTraceNote trims the edges and caps a run of blank
   lines at one, so the card holds only what was said. An empty result means
   "render nothing". */
export function tidyTraceNote(text: string | null | undefined): string {
  if (!text) return "";
  return text
    .replace(/\r\n?/g, "\n")
    .replace(/[ \t]+$/gm, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}
