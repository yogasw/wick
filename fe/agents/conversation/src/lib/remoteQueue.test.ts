import { describe, test, expect } from "vitest";
import { queueView, withRemoteLinks } from "./remoteQueue.js";
import { foldSystemEvents } from "./systemEvents.js";

type T = { turn_id?: string; role: string; kind?: string; text: string; extras?: Record<string, string> };
const q = (id: string, state: string, turn_id = id + state): T =>
  ({ turn_id, role: "system", kind: "remote_queue", text: "more", extras: { queue_id: id, state } });

describe("queueView", () => {
  test("queued names the agent and can be cancelled", () => {
    expect(queueView(q("a", "queued"), "Jules")).toEqual({ state: "queued", label: "Queued — sends after Jules finishes", canCancel: true });
  });
  test("forwarded and cancelled are final labels", () => {
    expect(queueView(q("a", "forwarded"))?.label).toBe("Forwarded");
    expect(queueView(q("a", "cancelled"))).toEqual({ state: "cancelled", label: "Cancelled", canCancel: false });
  });
  test("a sent message has no mark", () => {
    expect(queueView(q("a", "sent"))).toBeNull();
  });
});

describe("remote queue in the thread", () => {
  test("one row per message, latest state, sent dropped", () => {
    const turns: T[] = [
      { turn_id: "u", role: "user", text: "more" },
      q("a", "queued"),
      q("b", "queued"),
      q("a", "cancelled"),
      q("b", "sent"),
    ];
    const shown = withRemoteLinks(foldSystemEvents(turns));
    expect(shown.map((t) => t.extras?.state ?? t.role)).toEqual(["user", "cancelled"]);
  });
  test("the stop row gets the latest remote link; link rows are hidden", () => {
    const turns: T[] = [
      { role: "system", kind: "remote_link", text: "", extras: { url: "https://jules.google.com/session/1" } },
      { role: "assistant", text: "working" },
      { role: "system", kind: "interrupted", text: "stopped" },
    ];
    const shown = withRemoteLinks(turns);
    expect(shown).toHaveLength(2);
    expect(shown[1].extras?.remote_link).toBe("https://jules.google.com/session/1");
  });
  test("an unsafe link is not handed on", () => {
    const shown = withRemoteLinks<T>([
      { role: "system", kind: "remote_link", text: "", extras: { url: "javascript:alert(1)" } },
      { role: "system", kind: "interrupted", text: "stopped" },
    ]);
    expect(shown[0].extras?.remote_link).toBeUndefined();
  });
});
