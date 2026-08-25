import { describe, test, expect } from "vitest";
import { isViewer, setViewerId, viewerId } from "../viewer.js";

describe("isViewer", () => {
  // The point of matching on the WICK user id rather than the platform one:
  // the same human is U0104 in Slack and a uuid in the dashboard. Someone who
  // starts a thread in Slack and continues it on the web is one person, and
  // both of their messages have to read as theirs.
  test("recognises the same person across channels", () => {
    setViewerId("wick-1");
    expect(isViewer("wick-1")).toBe(true); // typed in the dashboard
    expect(isViewer("wick-1")).toBe(true); // …and sent from Slack
  });

  test("a different person is not the viewer", () => {
    setViewerId("wick-1");
    expect(isViewer("wick-2")).toBe(false);
  });

  // Unknown on either side must fall to "not me". Guessing the other way
  // would silently pass someone else's message off as yours, which is the
  // one mistake this check exists to prevent.
  test("unknown ids are never the viewer", () => {
    setViewerId("wick-1");
    expect(isViewer(undefined)).toBe(false);
    expect(isViewer("")).toBe(false);

    setViewerId("");
    expect(isViewer("wick-1")).toBe(false);
  });

  test("viewerId returns what was set", () => {
    setViewerId("wick-9");
    expect(viewerId()).toBe("wick-9");
  });
});
