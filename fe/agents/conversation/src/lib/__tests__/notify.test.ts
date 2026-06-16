import { describe, test, expect, vi } from "vitest";
import { notify } from "../notify.js";

describe("notify — focused tab (no-op)", () => {
  test("does not call showNotification or beep when hasFocus returns true", () => {
    const showNotification = vi.fn();
    const beep = vi.fn();

    notify("Hello", "world", { hasFocus: () => true, showNotification, beep });

    expect(showNotification).not.toHaveBeenCalled();
    expect(beep).not.toHaveBeenCalled();
  });

  test("no-ops with only title when focused", () => {
    const showNotification = vi.fn();
    const beep = vi.fn();

    notify("Agent needs input", undefined, { hasFocus: () => true, showNotification, beep });

    expect(showNotification).not.toHaveBeenCalled();
    expect(beep).not.toHaveBeenCalled();
  });
});

describe("notify — unfocused tab", () => {
  test("calls beep and showNotification with title and body when not focused", () => {
    const showNotification = vi.fn();
    const beep = vi.fn();

    notify("Agent needs your input", "Please approve", { hasFocus: () => false, showNotification, beep });

    expect(beep).toHaveBeenCalledOnce();
    expect(showNotification).toHaveBeenCalledOnce();
    expect(showNotification).toHaveBeenCalledWith("Agent needs your input", "Please approve");
  });

  test("calls showNotification with empty string when body is undefined", () => {
    const showNotification = vi.fn();
    const beep = vi.fn();

    notify("Hello", undefined, { hasFocus: () => false, showNotification, beep });

    expect(showNotification).toHaveBeenCalledWith("Hello", "");
  });

  test("uses fallback title when title is empty string", () => {
    const showNotification = vi.fn();
    const beep = vi.fn();

    notify("", undefined, { hasFocus: () => false, showNotification, beep });

    expect(showNotification).toHaveBeenCalledWith("Agent needs your input", "");
  });

  test("calls beep before showNotification", () => {
    const order: string[] = [];
    const showNotification = vi.fn(() => { order.push("notify"); });
    const beep = vi.fn(() => { order.push("beep"); });

    notify("Test", "body", { hasFocus: () => false, showNotification, beep });

    expect(order).toEqual(["beep", "notify"]);
  });
});
