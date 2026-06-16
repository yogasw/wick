import { describe, it, expect } from "vitest";
import { normalize, serialize, newField, newOp } from "../draft.js";
import type { Draft } from "$lib/types.js";

describe("normalize", () => {
  it("fills defaults from an empty object", () => {
    const d = normalize({});
    expect(d.key).toBe("");
    expect(d.icon).toBe("🔌");
    expect(d.source).toBe("manual");
    expect(d.configs).toEqual([]);
    expect(d.ops).toEqual([]);
  });

  it("coerces partial fields and ops to full shapes", () => {
    const d = normalize({
      key: "petstore",
      configs: [{ key: "base_url" } as never],
      ops: [{ key: "list", inputs: [{ key: "q" } as never] } as never],
    });
    expect(d.configs[0]).toEqual({
      key: "base_url",
      widget: "text",
      options: "",
      secret: false,
      required: false,
      default: "",
      desc: "",
    });
    expect(d.ops[0].inputs[0].key).toBe("q");
    expect(d.ops[0].request).toEqual({
      method: "GET",
      url_template: "",
      headers: {},
      body_template: "",
      content_type: "",
    });
  });

  it("keeps an mcp_source op without inventing a request", () => {
    const d = normalize({
      ops: [{ key: "x", mcp_source: { server_id: "s1", tool_name: "t1" } } as never],
    });
    expect(d.ops[0].request).toBeUndefined();
    expect(d.ops[0].mcp_source).toEqual({ server_id: "s1", tool_name: "t1" });
  });

  it("preserves an existing request and defaults its headers", () => {
    const d = normalize({
      ops: [{ key: "x", request: { method: "POST", url_template: "/u" } } as never],
    });
    expect(d.ops[0].request?.method).toBe("POST");
    expect(d.ops[0].request?.headers).toEqual({});
  });
});

describe("serialize", () => {
  it("round-trips a normalized draft and applies the icon fallback", () => {
    const d: Draft = normalize({ key: "k", icon: "" });
    const out = serialize(d);
    expect(out.icon).toBe("🔌");
    expect(out.key).toBe("k");
  });

  it("carries configs and ops through unchanged", () => {
    const d = normalize({ configs: [{ key: "a" } as never], ops: [{ key: "o" } as never] });
    const out = serialize(d);
    expect(out.configs).toHaveLength(1);
    expect(out.ops).toHaveLength(1);
  });
});

describe("newField / newOp", () => {
  it("newField is a blank text field", () => {
    expect(newField().widget).toBe("text");
  });

  it("newOp seeds a GET request and empty inputs", () => {
    const op = newOp();
    expect(op.request?.method).toBe("GET");
    expect(op.inputs).toEqual([]);
  });
});
