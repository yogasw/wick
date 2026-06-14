// Splits a Go-template string into ordered segments — literal text and
// {{ expression }} chunks — so the preview panel can evaluate each
// expression on its own and render a per-expression table. One bad
// expression then shows as empty/error in its row WITHOUT killing the
// whole preview (the old behaviour: a single nil-pointer ref failed the
// entire render, leaving the user guessing which ref was wrong).

export type TemplateSegment =
  | { kind: "text"; value: string }
  | { kind: "expr"; value: string; raw: string }; // value = inner trimmed, raw = full {{…}}

// splitTemplate walks the string and emits text/expr segments in order.
// Nested braces inside an expression are not supported by Go templates,
// so a simple {{ … }} scan is sufficient. An unclosed {{ at the end is
// emitted as text (the preview guard skips unbalanced input anyway).
export function splitTemplate(tmpl: string): TemplateSegment[] {
  const segs: TemplateSegment[] = [];
  let i = 0;
  while (i < tmpl.length) {
    const open = tmpl.indexOf("{{", i);
    if (open < 0) {
      if (i < tmpl.length) segs.push({ kind: "text", value: tmpl.slice(i) });
      break;
    }
    if (open > i) segs.push({ kind: "text", value: tmpl.slice(i, open) });
    const close = tmpl.indexOf("}}", open + 2);
    if (close < 0) {
      // Unclosed — treat the rest as literal text.
      segs.push({ kind: "text", value: tmpl.slice(open) });
      break;
    }
    const raw = tmpl.slice(open, close + 2);
    const inner = tmpl.slice(open + 2, close).trim();
    segs.push({ kind: "expr", value: inner, raw });
    i = close + 2;
  }
  return segs;
}

// Control-flow action keywords. A {{…}} starting with one of these is a
// block action (if/else/range/with/…), NOT a standalone value — it cannot
// be rendered in isolation (e.g. `{{ if x }}` alone is "unclosed action").
const CONTROL_KEYWORDS = new Set([
  "if", "else", "end", "range", "with", "define", "block", "template", "break", "continue",
]);

// isControlAction reports whether an expression's inner text is a Go
// template control action rather than a value interpolation.
export function isControlAction(inner: string): boolean {
  // First token (the keyword). `{{- if x }}` trim markers stripped first.
  const t = inner.replace(/^-/, "").replace(/-$/, "").trim();
  const first = t.split(/\s/)[0];
  return CONTROL_KEYWORDS.has(first);
}

// hasControlFlow reports whether the template uses any control-flow block.
// When it does, a per-expression preview is meaningless (the expressions
// depend on block scope), so the caller falls back to the full render.
export function hasControlFlow(tmpl: string): boolean {
  return splitTemplate(tmpl).some((s) => s.kind === "expr" && isControlAction(s.value));
}

// ExprSegment is one {{…}} chunk surfaced in the preview table. `isControl`
// flags control-flow keywords (if/else/end/range/with/…) — those are shown
// as a labelled "control flow" row but NOT evaluated standalone (they can't
// render on their own). Value segments are evaluated against the context.
export type ExprSegment = { value: string; raw: string; isControl: boolean };

// expressionSegments returns EVERY {{…}} chunk in order, each tagged with
// isControl. The preview table shows them all — value refs get evaluated
// (their result/error), control-flow keywords get a "control flow" label —
// so a mixed template (lots of {{.Event.Payload.x}} plus one {{ if … }}
// block, like the analyze prompt) shows its full structure. The caller
// only sends the value segments to the backend for rendering.
export function expressionSegments(tmpl: string): ExprSegment[] {
  return splitTemplate(tmpl)
    .filter((s): s is Extract<TemplateSegment, { kind: "expr" }> => s.kind === "expr")
    .map((s) => ({ value: s.value, raw: s.raw, isControl: isControlAction(s.value) }));
}
