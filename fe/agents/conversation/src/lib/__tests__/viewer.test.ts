import { describe, test, expect, afterEach } from "vitest";
import { isViewer, setViewerId, setViewerName, viewerId, viewerName } from "../viewer.js";

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

describe("viewerName", () => {
  const mount = (name?: string) => {
    const el = document.createElement("div");
    el.id = "app";
    if (name !== undefined) el.dataset.viewerName = name;
    document.body.appendChild(el);
    setViewerName(null);
  };
  afterEach(() => { document.getElementById("app")?.remove(); setViewerName(null); });

  test("reads data-viewer-name from #app", () => {
    mount("Yoga Setiawan");
    expect(viewerName()).toBe("Yoga Setiawan");
  });

  test("no attribute or no #app reads empty", () => {
    mount();
    expect(viewerName()).toBe("");
    document.getElementById("app")!.remove();
    setViewerName(null);
    expect(viewerName()).toBe("");
  });

  test("whitespace is trimmed, blank reads empty", () => {
    mount("  Yoga Setiawan \n");
    expect(viewerName()).toBe("Yoga Setiawan");
    document.getElementById("app")!.remove();
    mount("   ");
    expect(viewerName()).toBe("");
  });
});
