import { describe, it, expect } from "vitest";
import { splitTemplate, expressionSegments, isControlAction, hasControlFlow } from "./splitTemplate";

describe("splitTemplate", () => {
  it("splits the fetch_diff URL into text + 2 expressions", () => {
    const url = "https://api.github.com/repos/{{.Event.Payload.body.repository.full_name}}/pulls/{{.Event.Payload.body.pull_request.number}}";
    const segs = splitTemplate(url);
    expect(segs).toEqual([
      { kind: "text", value: "https://api.github.com/repos/" },
      { kind: "expr", value: ".Event.Payload.body.repository.full_name", raw: "{{.Event.Payload.body.repository.full_name}}" },
      { kind: "text", value: "/pulls/" },
      { kind: "expr", value: ".Event.Payload.body.pull_request.number", raw: "{{.Event.Payload.body.pull_request.number}}" },
    ]);
  });

  it("trims inner whitespace but keeps raw", () => {
    const segs = splitTemplate("{{ .Env.X | default \"y\" }}");
    expect(segs).toEqual([
      { kind: "expr", value: '.Env.X | default "y"', raw: '{{ .Env.X | default "y" }}' },
    ]);
  });

  it("pure text yields one text segment", () => {
    expect(splitTemplate("plain")).toEqual([{ kind: "text", value: "plain" }]);
  });

  it("emits an unclosed {{ as trailing text", () => {
    const segs = splitTemplate("a {{.Node.");
    expect(segs).toEqual([
      { kind: "text", value: "a " },
      { kind: "text", value: "{{.Node." },
    ]);
  });

  it("handles adjacent expressions", () => {
    const segs = splitTemplate("{{.a}}{{.b}}");
    expect(segs.filter((s) => s.kind === "expr")).toHaveLength(2);
  });
});

describe("expressionSegments", () => {
  it("returns only the expression chunks", () => {
    const exprs = expressionSegments("x {{.a.b}} y {{.c}} z");
    expect(exprs).toEqual([
      { value: ".a.b", raw: "{{.a.b}}", isControl: false },
      { value: ".c", raw: "{{.c}}", isControl: false },
    ]);
  });

  it("is empty for a literal-only template", () => {
    expect(expressionSegments("no exprs")).toEqual([]);
  });

  // Every {{…}} is returned, each tagged isControl. Control-flow keywords
  // (if/else/end) stay in the list (shown as a labelled row) but are
  // flagged so the caller doesn't evaluate them standalone.
  it("returns all segments, tagging control-flow keywords", () => {
    const tmpl = "{{ if .Node.fetch_diff.body }}{{ .Node.fetch_diff.body }}{{ else }}none{{ end }}";
    const exprs = expressionSegments(tmpl);
    expect(exprs).toEqual([
      { value: "if .Node.fetch_diff.body", raw: "{{ if .Node.fetch_diff.body }}", isControl: true },
      { value: ".Node.fetch_diff.body", raw: "{{ .Node.fetch_diff.body }}", isControl: false },
      { value: "else", raw: "{{ else }}", isControl: true },
      { value: "end", raw: "{{ end }}", isControl: true },
    ]);
  });

  it("tags range/end as control, inner ref as value", () => {
    const exprs = expressionSegments("{{ range .items }}{{ .id }}{{ end }}");
    expect(exprs.map((e) => [e.value, e.isControl])).toEqual([
      ["range .items", true],
      [".id", false],
      ["end", true],
    ]);
  });

  it("mixed prompt: value refs evaluated, if-block tagged control", () => {
    const tmpl = "Repo: {{.Event.Payload.body.repository.full_name}}\nDiff:\n{{ if .Node.fetch_diff.body }}{{ .Node.fetch_diff.body }}{{ else }}none{{ end }}";
    const value = expressionSegments(tmpl).filter((e) => !e.isControl).map((e) => e.value);
    expect(value).toEqual([
      ".Event.Payload.body.repository.full_name",
      ".Node.fetch_diff.body",
    ]);
  });

  it("pure value template has no control segments", () => {
    const exprs = expressionSegments("{{.a.b}}/{{.c}}");
    expect(exprs).toHaveLength(2);
    expect(exprs.every((e) => !e.isControl)).toBe(true);
  });
});

describe("isControlAction", () => {
  it("flags control keywords", () => {
    expect(isControlAction("if .x")).toBe(true);
    expect(isControlAction("else")).toBe(true);
    expect(isControlAction("end")).toBe(true);
    expect(isControlAction("range .items")).toBe(true);
    expect(isControlAction("with .x")).toBe(true);
  });

  it("does not flag value interpolations", () => {
    expect(isControlAction(".Node.fetch_diff.body")).toBe(false);
    expect(isControlAction(".Env.X | default \"y\"")).toBe(false);
    expect(isControlAction("now \"2006\"")).toBe(false);
  });

  it("handles trim markers", () => {
    expect(isControlAction("- if .x")).toBe(true);
  });
});

describe("hasControlFlow", () => {
  it("true for an if/end template", () => {
    expect(hasControlFlow("{{ if .x }}a{{ end }}")).toBe(true);
  });
  it("false for value-only", () => {
    expect(hasControlFlow("{{.a}}/{{.b}}")).toBe(false);
  });
});
