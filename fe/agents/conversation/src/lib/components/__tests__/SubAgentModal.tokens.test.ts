import { describe, test, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/* Guards the theme contract, not the look: every navy/black/white shade in the
   component must exist in tailwind.config.js, because a class built from a
   shade outside the palette (e.g. dark:bg-navy-900) is silently never
   generated — the element then keeps its light-mode color in dark themes,
   which is exactly the "modal ignores the theme" bug this test pins down. */

const PALETTE: Record<string, [number, number]> = {
  navy: [200, 800],
  black: [600, 900],
  white: [100, 400],
};

function themedTokens(source: string): string[] {
  return source.match(/\b[a-z-]*(?:navy|black|white)-\d{3}\b/g) ?? [];
}

describe("SubAgentModal theme tokens", () => {
  test("uses only palette-backed navy/black/white shades", () => {
    const src = readFileSync(
      resolve(process.cwd(), "src/lib/components/SubAgentModal.svelte"),
      "utf8",
    );
    const offenders = themedTokens(src).filter((cls) => {
      const m = /(navy|black|white)-(\d{3})$/.exec(cls);
      if (!m) return false;
      const [lo, hi] = PALETTE[m[1]];
      const shade = Number(m[2]);
      return shade < lo || shade > hi;
    });
    expect(offenders).toEqual([]);
  });
});
