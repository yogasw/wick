import { describe, it, expect } from "vitest";
import { outputPorts, portCenterX, cardWidth, flatPortLabels } from "./ports";
import { nodeOutPorts, inputPorts, ERROR_KEY } from "./ports";
import { applyCaseRename } from "$lib/stores/editor";
import type { Edge, Node } from "$lib/types/workflow";

const node = (n: Partial<Node>): Node => ({ id: "n", type: "branch", ...n }) as Node;

describe("outputPorts", () => {
  it("is empty for a single-output node", () => {
    expect(outputPorts(node({ type: "agent" }), [])).toEqual([]);
  });

  it("always lists both datatable_get verdicts", () => {
    expect(outputPorts(node({ type: "datatable_get" }), [])).toEqual(["found", "not_found"]);
    expect(outputPorts(node({ type: "datatable_exists" }), [])).toEqual(["true", "false"]);
  });

  it("puts declared branch outputs first, edge-only cases next, default last", () => {
    const edges: Edge[] = [
      { from: "n", to: "a", case: "default" },
      { from: "n", to: "b", case: "legacy" },
      { from: "n", to: "c", case: "fresh" },
      { from: "x", to: "d", case: "other" },
    ];
    expect(outputPorts(node({ output_cases: ["fresh", "stale"] }), edges)).toEqual(["fresh", "stale", "legacy", "default"]);
  });

  it("gives a fresh branch a default port", () => {
    expect(outputPorts(node({}), [])).toEqual(["default"]);
  });

  it("uses switch rule cases and its default_case", () => {
    const n = node({ type: "switch", cases: [{ when: "a", case: "dm" }], default_case: "other" });
    expect(outputPorts(n, [])).toEqual(["dm", "other"]);
  });
});

describe("portCenterX", () => {
  it("splits the card width into equal columns", () => {
    expect(portCenterX(0, 220, 1, 0)).toBe(110);
    expect(portCenterX(100, 220, 2, 0)).toBe(155);
    expect(portCenterX(100, 220, 2, 1)).toBe(265);
  });
});

describe("applyCaseRename", () => {
  it("retags only that node's edges with the old case", () => {
    const edges: Edge[] = [
      { from: "n", to: "a", case: "old" },
      { from: "n", to: "b", case: "default" },
      { from: "m", to: "c", case: "old" },
    ];
    expect(applyCaseRename(edges, "n", "old", "new").map((e) => e.case)).toEqual(["new", "default", "old"]);
  });
});

describe("nodeOutPorts", () => {
  it("adds success + error when failures fall back", () => {
    const n = { id: "a", type: "connector", on_failure: "fallback", fallback: "b" } as any;
    expect(nodeOutPorts(n, []).map((p) => p.label)).toEqual(["success", "error"]);
    expect(nodeOutPorts(n, [])[0].key).toBe("");
    expect(nodeOutPorts(n, [])[1].key).toBe(ERROR_KEY);
  });
  it("keeps cases and appends error for a routed node", () => {
    const n = { id: "a", type: "datatable_get", on_failure: "fallback" } as any;
    expect(nodeOutPorts(n, []).map((p) => p.label)).toEqual(["found", "not_found", "error"]);
  });
  it("is empty for a plain single-output node", () => {
    expect(nodeOutPorts({ id: "a", type: "connector" } as any, [])).toEqual([]);
  });
});

describe("inputPorts", () => {
  const wf = (nodes: any[], edges: any[], triggers: any[] = []) =>
    ({ triggers, graph: { nodes, edges } }) as any;

  it("is empty with a single source", () => {
    const w = wf([{ id: "a", _canvas: { x: 0 } }, { id: "b" }], [{ from: "a", to: "b" }]);
    expect(inputPorts(w.graph.nodes[1], w)).toEqual([]);
  });
  it("lists a success edge and a failure path separately, ordered by x", () => {
    const w = wf(
      [
        { id: "ok", _canvas: { x: 400 } },
        { id: "bad", _canvas: { x: 0 }, on_failure: "fallback", fallback: "c" },
        { id: "c" },
      ],
      [{ from: "ok", to: "c" }],
    );
    const ins = inputPorts(w.graph.nodes[2], w);
    expect(ins.map((p) => [p.from, p.detail])).toEqual([["bad", "error"], ["ok", undefined]]);
  });
  it("lists triggers and unwired merge inputs", () => {
    const w = wf(
      [{ id: "a", _canvas: { x: 0 } }, { id: "b", _canvas: { x: 300 } }, { id: "m", type: "merge", inputs: ["a", "b"] }],
      [{ from: "a", to: "m" }],
      [{ id: "t1", type: "manual", entry_node: "m" }],
    );
    const ins = inputPorts(w.graph.nodes[2], w);
    expect(ins.map((p) => [p.key, p.connected])).toEqual([
      ["e:a:", true],
      ["t:t1", true],
      ["e:b:", false],
    ]);
  });
});

describe("cardWidth", () => {
  it("keeps 220px for few ports and widens per port up to the cap", () => {
    expect(cardWidth(0)).toBe(220);
    expect(cardWidth(2)).toBe(220);
    expect(cardWidth(4)).toBe(360);
    expect(cardWidth(6)).toBe(540);
    expect(flatPortLabels(6)).toBe(true);
    expect(cardWidth(7)).toBe(220);
    expect(flatPortLabels(7)).toBe(false);
  });

  it("widens for inputs too, whichever side needs more room", () => {
    expect(cardWidth(1, 4)).toBe(360);
    expect(cardWidth(5, 3)).toBe(450);
    expect(cardWidth(7, 3)).toBe(270);
    expect(cardWidth(2, 8)).toBe(220);
  });
});
