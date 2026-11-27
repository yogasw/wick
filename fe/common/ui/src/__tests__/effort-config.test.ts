import { describe, expect, it, vi } from "vitest";
import { effortDefaultLabel, effortPickerConfig, type EffortInfo } from "../effort-config";

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

  it("shows a pick the model no longer offers as Default", () => {
    expect(effortPickerConfig(info({ requested: "xhigh", stale: true }), () => {})!.value).toBe("default");
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
