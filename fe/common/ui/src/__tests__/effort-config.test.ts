import { describe, expect, it, vi } from "vitest";
import { effortClampMessage, effortDefaultLabel, effortPickerConfig, nearestEffort, type EffortInfo } from "../effort-config";

const info = (over: Partial<EffortInfo> = {}): EffortInfo => ({
  configurable: true,
  options: [
    { id: "low", label: "Low" },
    { id: "high", label: "High" },
  ],
  requested: "",
  ...over,
});

describe("effortPickerConfig", () => {
  it("hides when the model has nothing to pick", () => {
    expect(effortPickerConfig(null, () => {})).toBeUndefined();
    expect(effortPickerConfig(info({ configurable: false }), () => {})).toBeUndefined();
    expect(effortPickerConfig(info({ options: [] }), () => {})).toBeUndefined();
  });

  it("leads with Default and shows the session pick", () => {
    const cfg = effortPickerConfig(info({ requested: "high" }), () => {})!;
    expect(cfg.options.map((o) => o.id)).toEqual(["default", "low", "high"]);
    expect(cfg.value).toBe("high");
  });

  it("falls back to the inherited default, then Default", () => {
    expect(effortPickerConfig(info({ inherited: "low" }), () => {})!.value).toBe("low");
    expect(effortPickerConfig(info(), () => {})!.value).toBe("default");
  });

  it("shows a pick the model does not offer as the nearest level it does", () => {
    expect(effortPickerConfig(info({ requested: "xhigh", clamped: true }), () => {})!.value).toBe("high");
    expect(effortPickerConfig(info({ inherited: "max" }), () => {})!.value).toBe("high");
  });

  it("shows an off-scale pick as Default", () => {
    expect(effortPickerConfig(info({ requested: "turbo", stale: true }), () => {})!.value).toBe("default");
  });

  it("keeps a disabled gauge for a model without effort only when asked", () => {
    expect(effortPickerConfig(info({ configurable: false, options: [] }), () => {})).toBeUndefined();
    const cfg = effortPickerConfig(info({ configurable: false, options: [] }), () => {}, { showUnavailable: true })!;
    expect(cfg.disabledReason).toBe("This model has no effort setting");
    expect(cfg.options).toHaveLength(1);
  });

  it("passes the known default for the gauge and forwards changes", () => {
    const onChange = vi.fn();
    const cfg = effortPickerConfig(info({ default: "high" }), onChange)!;
    expect(cfg.defaultValue).toBe("high");
    cfg.onChange("low");
    expect(onChange).toHaveBeenCalledWith("low");
  });
});

describe("effortDefaultLabel", () => {
  it("names a known default, never invents one", () => {
    expect(effortDefaultLabel(info({ default: "high" }))).toBe("Default (High)");
    expect(effortDefaultLabel(info())).toBe("Provider default");
  });
});

describe("nearestEffort", () => {
  const opts = [{ id: "low" }, { id: "medium" }, { id: "high" }];
  it("takes the highest level not above the pick, else the lowest above", () => {
    expect(nearestEffort(opts, "max")).toBe("high");
    expect(nearestEffort(opts, "minimal")).toBe("low");
    expect(nearestEffort(opts, "medium")).toBe("medium");
  });
  it("has no answer off the scale", () => {
    expect(nearestEffort(opts, "turbo")).toBe("");
    expect(nearestEffort([{ id: "fast" }], "high")).toBe("");
  });
});

describe("effortClampMessage", () => {
  it("names both levels when the pick runs lower", () => {
    expect(effortClampMessage(info({ requested: "max" }))).toBe("Max isn't available on this model — using High");
    expect(effortClampMessage(info(), "xhigh")).toBe("Extra High isn't available on this model — using High");
  });
  it("is empty when the pick runs as picked or is Default", () => {
    expect(effortClampMessage(info({ requested: "high" }))).toBe("");
    expect(effortClampMessage(info({ requested: "default" }))).toBe("");
    expect(effortClampMessage(info())).toBe("");
  });
});
