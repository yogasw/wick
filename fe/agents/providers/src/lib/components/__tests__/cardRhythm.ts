/* cardRhythm.ts — the rule that the "cards stuck together" bug broke.
 *
 * Every settings page here is one vertical stack of cards, and the gap
 * between them comes from ONE class on the stack (`space-y-4`). Tailwind's
 * `space-y-*` only reaches an element's own children, so the moment
 * somebody wraps a run of cards in a plain <div> — to branch on a
 * condition, to add a testid — the wrapper swallows the rhythm and every
 * card below it sits flush against the next. Nothing errors, nothing
 * looks wrong in the diff, and it is only caught when a person squints
 * at a screenshot.
 *
 * So this is asserted structurally rather than reviewed by eye: any
 * element that stacks two or more cards has to say how they are spaced.
 * A component test calls expectCardRhythm(container) and gets the same
 * check for free every time the markup moves.
 */

/** A card is the recurring surface of these pages: rounded box, own
 *  border, own background. `rounded-xl` is the part that never varies. */
const CARD = /(^|\s)rounded-xl(\s|$)/;

/** Ways of spacing children that all count as an answer. `divide-y`
 *  included: a stack that draws rules between its rows is a deliberate
 *  flush stack, not an accident. */
const RHYTHM = /(^|\s)(space-y-(?:px|\d)|gap-(?:px|\d)|gap-y-(?:px|\d)|divide-y)/;

/** An element may opt out when flush IS the design, e.g. a list whose
 *  rows carry their own borders. Explicit, so it shows up in review. */
const OPT_OUT = "data-flush-stack";

function classOf(el: Element): string {
  return el.getAttribute("class") ?? "";
}

/** cardStacksMissingRhythm returns a readable description of every
 *  element that stacks cards without spacing them. Empty means the tree
 *  is fine. */
export function cardStacksMissingRhythm(root: Element): string[] {
  const bad: string[] = [];
  const visit = (el: Element) => {
    const cards = Array.from(el.children).filter((c) => CARD.test(classOf(c)));
    if (cards.length >= 2 && !el.hasAttribute(OPT_OUT) && !RHYTHM.test(classOf(el))) {
      const id = el.getAttribute("data-testid");
      bad.push(
        `<${el.tagName.toLowerCase()}${id ? ` data-testid="${id}"` : ""} class="${classOf(el)}"> ` +
          `stacks ${cards.length} cards with no space-y/gap/divide-y`,
      );
    }
    for (const c of Array.from(el.children)) visit(c);
  };
  visit(root);
  return bad;
}

/** expectCardRhythm fails the test with the offending element spelled
 *  out, so the fix is "add space-y-4 here" rather than a hunt. */
export function expectCardRhythm(root: Element): void {
  const bad = cardStacksMissingRhythm(root);
  if (bad.length > 0) {
    throw new Error(
      "cards are stacked without vertical rhythm (they will render flush against each other):\n  " +
        bad.join("\n  "),
    );
  }
}
