import { describe, it, expect, beforeEach, vi } from "vitest";
import { get } from "svelte/store";
import {
  parseSavedResets,
  expiresSoon,
  savedResetsTooltip,
  showSavedResetsChip,
  usableNowText,
  inCooldown,
  ringTone,
} from "../usage/base/savedResets.js";
import { glanceFromWire, loadUsage, peekUsage, resetUsageStore, seedUsage, usageStore } from "../usage/base/usageStore.js";

const DAY = 86_400_000;
const now = Date.parse("2026-10-09T12:00:00Z");

describe("parseSavedResets", () => {
  it("is null when the field is absent", () => {
    expect(parseSavedResets(undefined)).toBeNull();
    expect(parseSavedResets(null)).toBeNull();
  });

  it("maps the wire shape", () => {
    const r = parseSavedResets({
      supported: true,
      available: 2,
      items: [{ id: "a", label: "Full reset", expires_at: "2026-10-14T00:00:00Z", usable_now: true }],
      hint: "Use it from the CLI.",
    })!;
    expect(r.available).toBe(2);
    expect(r.items[0]).toMatchObject({ id: "a", label: "Full reset", usableNow: true, requiresLimit: false });
    expect(r.hint).toBe("Use it from the CLI.");
  });
});

describe("chip + tooltip", () => {
  const soon = parseSavedResets({ supported: true, available: 1, items: [{ expires_at: new Date(now + 2 * DAY).toISOString() }] });
  const later = parseSavedResets({ supported: true, available: 2, items: [{ expires_at: new Date(now + 10 * DAY).toISOString() }] });

  it("hides with nothing to spend", () => {
    expect(showSavedResetsChip(parseSavedResets({ supported: true, available: 0 }))).toBe(false);
    expect(showSavedResetsChip(null)).toBe(false);
    expect(showSavedResetsChip(later)).toBe(true);
  });

  it("turns amber within 3 days", () => {
    expect(expiresSoon(soon, now)).toBe(true);
    expect(expiresSoon(later, now)).toBe(false);
  });

  it("says count and use-by date", () => {
    expect(savedResetsTooltip(soon)).toMatch(/^1 saved reset · use by /);
    expect(savedResetsTooltip(later)).toMatch(/^2 saved resets · use by /);
  });
});

describe("section states", () => {
  it("nothing to spend hides the section", () => {
    expect(showSavedResetsChip(parseSavedResets({ supported: true, available: 0, note: "Not available for this account." }))).toBe(false);
    expect(showSavedResetsChip(parseSavedResets({ supported: true, available: 0 }))).toBe(false);
  });

  it("usable now / at a limit / cooldown", () => {
    const atLimit = parseSavedResets({ supported: true, available: 1, items: [{ requires_limit: true }] });
    expect(usableNowText(atLimit, now)).toBe("Only at a limit");
    const yes = parseSavedResets({ supported: true, available: 1, items: [{ usable_now: true }] });
    expect(usableNowText(yes, now)).toBe("Yes");
    const cool = parseSavedResets({ supported: true, available: 1, items: [{ usable_now: true }], cooldown_until: new Date(now + DAY).toISOString() });
    expect(inCooldown(cool, now)).toBe(true);
    expect(usableNowText(cool, now)).toBe("No");
  });
});

describe("ringTone", () => {
  it("green < 80, amber 80–99, red ≥ 100", () => {
    expect(ringTone(79)).toBe("ok");
    expect(ringTone(80)).toBe("warn");
    expect(ringTone(99)).toBe("warn");
    expect(ringTone(100)).toBe("full");
  });
});

describe("usage store", () => {
  beforeEach(() => {
    resetUsageStore();
    vi.restoreAllMocks();
  });

  it("serves a seeded reading without fetching", async () => {
    const f = vi.spyOn(globalThis, "fetch");
    seedUsage("claude/main", glanceFromWire({ supported: true, windows: [{ key: "five_hour", utilization: 12 }] }));
    await loadUsage("", "claude/main");
    expect(f).not.toHaveBeenCalled();
    expect(peekUsage("claude/main")?.windows[0].utilization).toBe(12);
  });

  it("dedups concurrent loads and parses saved_resets", async () => {
    const f = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ supported: true, windows: [{ key: "seven_day", utilization: 90 }], saved_resets: { supported: true, available: 1 } }), {
        status: 200,
      }),
    );
    await Promise.all([loadUsage("/b", "codex/x"), loadUsage("/b", "codex/x")]);
    expect(f).toHaveBeenCalledTimes(1);
    expect(f.mock.calls[0][0]).toBe("/b/api/composer/usage?provider=codex%2Fx");
    const g = get(usageStore)["codex/x"].glance;
    expect(g.checked).toBe(true);
    expect(g.savedResets?.available).toBe(1);
  });

  it("marks a pending reading as not checked", () => {
    expect(glanceFromWire({ supported: true, pending: true }).checked).toBe(false);
  });
});
