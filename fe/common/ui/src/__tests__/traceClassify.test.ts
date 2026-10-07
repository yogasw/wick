import { describe, it, expect } from "vitest";
// The Go fixture itself — not a copy — so the two classifiers cannot drift.
import cases from "../../../../../internal/agents/event/testdata/classify_cases.json";
import { classifyCall, classifyResult, toolFamily, langForPath } from "../trace/classify.js";

type Case = {
  name: string; tool: string; call?: boolean; is_error?: boolean; input: string; kind: string;
  mime?: string; lang?: string; command?: string; summary?: string; cwd?: string; path?: string;
  pattern?: string; connector?: string; op?: string; parts?: string[];
};

describe("classifyTrace parity with event.Classify", () => {
  for (const c of cases as Case[]) {
    it(c.name, () => {
      const d = c.call ? classifyCall(c.tool, c.input) : classifyResult(c.tool, c.input, !!c.is_error);
      expect(d.kind).toBe(c.kind);
      for (const f of ["mime", "lang", "command", "summary", "cwd", "path", "pattern", "connector", "op"] as const) {
        if (c[f]) expect(d[f], f).toBe(c[f]);
      }
      if (c.parts) expect((d.parts ?? []).map((p) => p.kind)).toEqual(c.parts);
    });
  }
});

describe("classifyTrace binaries", () => {
  it("never decodes: size from base64 length, payload kept in data", () => {
    const b64 = "iVBORw0KGgo" + "A".repeat(4000);
    const d = classifyResult("", b64);
    expect(d.kind).toBe("image");
    expect(d.mime).toBe("image/png");
    expect(d.data).toBe(b64);
    expect(d.original_bytes).toBe(Math.floor((b64.length * 3) / 4));
    expect(d.summary).toMatch(/^PNG · \d+ KB$/);
  });

  it("names a read binary after the read call's path", () => {
    const call = classifyCall("Read", JSON.stringify({ file_path: "/r/shot.png" }));
    const d = classifyResult("Read", "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB", false, call);
    expect(d.name).toBe("shot.png");
  });
});

describe("helpers", () => {
  it("toolFamily / langForPath", () => {
    expect(toolFamily("functions.exec_command")).toBe("bash");
    expect(toolFamily("github.get_issue")).toBe("mcp");
    expect(toolFamily("TodoWrite")).toBe("");
    expect(langForPath("a/Dockerfile")).toBe("dockerfile");
    expect(langForPath("x.svelte")).toBe("svelte");
  });
});
