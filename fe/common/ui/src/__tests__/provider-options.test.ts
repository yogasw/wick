import { describe, test, expect } from "vitest";
import { buildProviderOptions } from "../provider-options.js";

describe("buildProviderOptions", () => {
  test("labels the canonical instance by its type alone", () => {
    const opts = buildProviderOptions([{ type: "claude", name: "claude" }], "");
    expect(opts[0].value).toBe("claude/claude");
    expect(opts[0].label).toBe("Claude");
  });

  test("keeps the full key as the label for a named instance", () => {
    const opts = buildProviderOptions([{ type: "codex", name: "abc" }], "");
    expect(opts[0].value).toBe("codex/abc");
    expect(opts[0].label).toBe("codex/abc");
  });

  test("carries an instance's models through", () => {
    const opts = buildProviderOptions(
      [{ type: "wick", name: "wick", models: [{ id: "m1", label: "M1", default: true }] }],
      "",
    );
    expect(opts[0].models?.[0].id).toBe("m1");
  });

  // A deleted or renamed instance must stay selectable, or opening the
  // form would silently move a saved role onto some other provider.
  test("surfaces a saved value that is no longer offered", () => {
    const opts = buildProviderOptions([{ type: "claude", name: "claude" }], "codex/gone::m9");
    const last = opts[opts.length - 1];
    expect(last.value).toBe("codex/gone");
    expect(last.label).toContain("unavailable");
  });

  test("does not duplicate a saved value that is still offered", () => {
    const opts = buildProviderOptions([{ type: "claude", name: "claude" }], "claude/claude::m1");
    expect(opts).toHaveLength(1);
  });

  // Every role stored before instances existed holds a bare type. It must
  // match its canonical instance, not render as a phantom "(unavailable)".
  test("a legacy bare provider matches its canonical instance", () => {
    const opts = buildProviderOptions([{ type: "claude", name: "claude" }], "claude");
    expect(opts).toHaveLength(1);
  });

  // Dropping `live` made a model SET look like a single selectable model,
  // whose id resolves to nothing runnable — the picker could never reach the
  // set's actual leaves.
  test("carries the live-set flag through so the picker can expand it", () => {
    const opts = buildProviderOptions(
      [
        {
          type: "wick",
          name: "x",
          models: [
            { id: "set1", label: "Gemini (live)", default: false, live: true },
            { id: "m1", label: "M1", default: true },
          ],
        },
      ],
      "",
    );
    expect(opts[0].models?.[0].live).toBe(true);
    expect(opts[0].models?.[1].live).toBeUndefined();
  });
});
