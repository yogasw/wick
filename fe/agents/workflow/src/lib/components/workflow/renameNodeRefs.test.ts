import { describe, it, expect } from "vitest";
import { rewriteNodeRef, renameNodeRefs, type GraphNode } from "./renameNodeRefs";

describe("rewriteNodeRef", () => {
  it("rewrites a whole-segment ref", () => {
    expect(rewriteNodeRef("{{.Node.fetch.body}}", "fetch", "download")).toBe("{{.Node.download.body}}");
  });

  it("rewrites a bare ref ending in }}", () => {
    expect(rewriteNodeRef("{{.Node.fetch}}", "fetch", "download")).toBe("{{.Node.download}}");
  });

  it("does NOT corrupt a longer label that shares a prefix", () => {
    // renaming "check" must not touch ".Node.check_action"
    expect(rewriteNodeRef("{{.Node.check_action.x}}", "check", "gate")).toBe("{{.Node.check_action.x}}");
  });

  it("rewrites multiple occurrences", () => {
    const s = "{{.Node.a.x}} and {{.Node.a.y}}";
    expect(rewriteNodeRef(s, "a", "b")).toBe("{{.Node.b.x}} and {{.Node.b.y}}");
  });

  it("rewrites the index .Node \"label\" form", () => {
    expect(rewriteNodeRef('{{ index .Node "my node" "x" }}', "my node", "renamed"))
      .toBe('{{ index .Node "renamed" "x" }}');
  });

  it("no-op when old == new or label absent", () => {
    expect(rewriteNodeRef("{{.Node.a.x}}", "a", "a")).toBe("{{.Node.a.x}}");
    expect(rewriteNodeRef("{{.Node.b.x}}", "a", "c")).toBe("{{.Node.b.x}}");
  });
});

describe("renameNodeRefs", () => {
  // Mirrors the pr-ai-review workflow: rename check_action and watch
  // fetch_diff's url + analyze's prompt follow.
  const nodes: GraphNode[] = [
    { id: "check_action", label: "check_action", expression: "{{.Event.Payload.body.action}}" },
    {
      id: "fetch_diff",
      label: "fetch_diff",
      url: "https://x/{{.Node.check_action.result}}",
      headers: { "X-Ref": "{{.Node.check_action.result}}" },
    },
    {
      id: "analyze",
      label: "analyze",
      prompt: "diff:\n{{.Node.fetch_diff.body}}\naction was {{.Node.check_action.result}}",
    },
  ];

  it("updates the renamed node's own label", () => {
    const out = renameNodeRefs(nodes, "check_action", "check_action", "gate");
    expect(out.find((n) => n.id === "check_action")!.label).toBe("gate");
  });

  it("rewrites refs to the renamed label across all nodes (incl nested headers)", () => {
    const out = renameNodeRefs(nodes, "check_action", "check_action", "gate");
    const fetch = out.find((n) => n.id === "fetch_diff")!;
    expect(fetch.url).toBe("https://x/{{.Node.gate.result}}");
    expect((fetch.headers as Record<string, string>)["X-Ref"]).toBe("{{.Node.gate.result}}");
    const analyze = out.find((n) => n.id === "analyze")!;
    expect(analyze.prompt).toContain("{{.Node.gate.result}}");
    // unrelated ref left intact
    expect(analyze.prompt).toContain("{{.Node.fetch_diff.body}}");
  });

  it("does not mutate the input array", () => {
    const before = JSON.stringify(nodes);
    renameNodeRefs(nodes, "check_action", "check_action", "gate");
    expect(JSON.stringify(nodes)).toBe(before);
  });

  it("leaves the renamed node's own identity refs alone but renames others", () => {
    // a node that references ITSELF by old label still gets rewritten in
    // body fields (only id/label keys are protected).
    const ns: GraphNode[] = [
      { id: "n1", label: "old", prompt: "self {{.Node.old.x}}" },
    ];
    const out = renameNodeRefs(ns, "n1", "old", "new");
    expect(out[0].label).toBe("new");
    expect(out[0].prompt).toBe("self {{.Node.new.x}}");
  });
});
