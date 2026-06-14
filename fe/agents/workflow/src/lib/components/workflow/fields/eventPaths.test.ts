import { describe, it, expect } from "vitest";
import { eventPayloadPaths, directChildren, buildPreviewRequest, bracesBalanced } from "./eventPaths";

// The webhook envelope a github_pr run carries: body is nested, which is
// exactly why the static PAYLOAD_KEYS list (text/user/channel_id) missed
// .body.action and the user couldn't autocomplete it.
const ENVELOPE = {
  body: { action: "opened", number: 716, pull_request: { title: "x" } },
  method: "POST",
  headers: { "Content-Type": "application/json" },
};

describe("eventPayloadPaths", () => {
  it("includes top-level keys", () => {
    const paths = eventPayloadPaths(ENVELOPE);
    expect(paths).toContain(".Event.Payload.body");
    expect(paths).toContain(".Event.Payload.method");
    expect(paths).toContain(".Event.Payload.headers");
  });

  it("includes nested body keys (the missing .body.action)", () => {
    const paths = eventPayloadPaths(ENVELOPE);
    expect(paths).toContain(".Event.Payload.body.action");
    expect(paths).toContain(".Event.Payload.body.number");
    expect(paths).toContain(".Event.Payload.body.pull_request.title");
  });

  it("respects maxDepth", () => {
    const deep = { a: { b: { c: { d: { e: 1 } } } } };
    const paths = eventPayloadPaths(deep, 2);
    expect(paths).toContain(".Event.Payload.a");
    expect(paths).toContain(".Event.Payload.a.b");
    expect(paths).toContain(".Event.Payload.a.b.c");
    expect(paths).not.toContain(".Event.Payload.a.b.c.d.e");
  });

  it("descends the first array element", () => {
    const paths = eventPayloadPaths({ items: [{ id: 1 }] });
    expect(paths).toContain(".Event.Payload.items");
    expect(paths).toContain(".Event.Payload.items.0.id");
  });

  it("is empty for null/scalar", () => {
    expect(eventPayloadPaths(null)).toEqual([]);
    expect(eventPayloadPaths(42)).toEqual([]);
  });
});

describe("directChildren", () => {
  const all = eventPayloadPaths(ENVELOPE);

  it("lists immediate children of .Event.Payload.", () => {
    const kids = directChildren(all, ".Event.Payload.");
    expect(kids).toContain(".Event.Payload.body");
    expect(kids).toContain(".Event.Payload.method");
    // not the grandchild
    expect(kids).not.toContain(".Event.Payload.body.action");
  });

  it("lists immediate children of a nested prefix", () => {
    const kids = directChildren(all, ".Event.Payload.body.");
    expect(kids).toContain(".Event.Payload.body.action");
    expect(kids).toContain(".Event.Payload.body.number");
    expect(kids).toContain(".Event.Payload.body.pull_request");
    expect(kids).not.toContain(".Event.Payload.body.pull_request.title");
  });
});

describe("buildPreviewRequest", () => {
  it("sends the real replayed event in context and clears sample_event", () => {
    const req = buildPreviewRequest({}, ENVELOPE, "cron");
    expect(req.sampleEvent).toBe(""); // must NOT clobber context.Event
    const ctx = JSON.parse(req.context!);
    expect(ctx.Event.payload).toEqual(ENVELOPE);
    expect(ctx.Event.type).toBe("webhook");
  });

  it("includes node outputs alongside the event", () => {
    const req = buildPreviewRequest({ check_action: { result: "opened" } }, ENVELOPE, "cron");
    const ctx = JSON.parse(req.context!);
    expect(ctx.Node.check_action).toEqual({ result: "opened" });
    expect(ctx.Event.payload).toEqual(ENVELOPE);
  });

  it("falls back to sample_event when no run replayed", () => {
    const req = buildPreviewRequest({}, null, "cron");
    expect(req.sampleEvent).toBe("cron");
    expect(req.context).toBeUndefined();
  });

  it("keeps node context but uses sample_event when no event", () => {
    const req = buildPreviewRequest({ n1: { x: 1 } }, null, "slack.message");
    expect(req.sampleEvent).toBe("slack.message");
    const ctx = JSON.parse(req.context!);
    expect(ctx.Node.n1).toEqual({ x: 1 });
    expect(ctx.Event).toBeUndefined();
  });
});

describe("bracesBalanced", () => {
  it("true when every {{ has a }}", () => {
    expect(bracesBalanced("{{.Event.Payload.body.action}}")).toBe(true);
    expect(bracesBalanced(".../{{.a.b}}/.../{{.c.d}}")).toBe(true);
    expect(bracesBalanced("no template here")).toBe(true);
  });

  it("false mid-type with an unclosed {{ (the unclosed-action flash)", () => {
    expect(bracesBalanced("{{.Event.Payload.body}}\n\n{{.Node.")).toBe(false);
    expect(bracesBalanced("{{.a")).toBe(false);
  });
});
