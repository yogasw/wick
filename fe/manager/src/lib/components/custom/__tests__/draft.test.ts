import { describe, it, expect } from "vitest";
import { normalize, serialize, newField, newOp } from "../draft.js";
import type { Draft } from "$lib/types.js";

describe("normalize", () => {
  it("fills defaults from an empty object with one default section", () => {
    const d = normalize({});
    expect(d.key).toBe("");
    expect(d.icon).toBe("🔌");
    expect(d.source).toBe("manual");
    expect(d.configs).toEqual([]);
    /* A fresh draft always carries one untitled section to add ops into. */
    expect(d.ops).toHaveLength(1);
    expect(d.ops[0]).toEqual({ title: "", description: "", ops: [] });
  });

  it("coerces partial fields and nested ops to full shapes", () => {
    const d = normalize({
      key: "petstore",
      configs: [{ key: "base_url" } as never],
      ops: [{ title: "Pets", ops: [{ key: "list", inputs: [{ key: "q" } as never] }] } as never],
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
    expect(d.ops[0].title).toBe("Pets");
    const op = d.ops[0].ops[0];
    expect(op.inputs[0].key).toBe("q");
    expect(op.request).toEqual({
      method: "GET",
      url_template: "",
      headers: {},
      body_template: "",
      content_type: "",
    });
  });

  it("keeps an mcp_source op without inventing a request", () => {
    const d = normalize({
      ops: [{ ops: [{ key: "x", mcp_source: { server_id: "s1", tool_name: "t1" } }] } as never],
    });
    const op = d.ops[0].ops[0];
    expect(op.request).toBeUndefined();
    expect(op.mcp_source).toEqual({ server_id: "s1", tool_name: "t1" });
  });

  it("preserves an existing request and defaults its headers", () => {
    const d = normalize({
      ops: [{ ops: [{ key: "x", request: { method: "POST", url_template: "/u" } }] } as never],
    });
    const op = d.ops[0].ops[0];
    expect(op.request?.method).toBe("POST");
    expect(op.request?.headers).toEqual({});
  });
});

describe("serialize", () => {
  it("round-trips a normalized draft and applies the icon fallback", () => {
    const d: Draft = normalize({ key: "k", icon: "" });
    const out = serialize(d);
    expect(out.icon).toBe("🔌");
    expect(out.key).toBe("k");
  });

  it("carries configs and op sections through unchanged", () => {
    const d = normalize({ configs: [{ key: "a" } as never], ops: [{ ops: [{ key: "o" }] } as never] });
    const out = serialize(d);
    expect(out.configs).toHaveLength(1);
    expect(out.ops).toHaveLength(1);
    expect(out.ops[0].ops).toHaveLength(1);
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
