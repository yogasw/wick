import { describe, it, expect } from "vitest";
import { nodeRegistry, componentFor } from "./index";
import type { NodeType } from "$lib/types/workflow";

describe("node registry", () => {
  it("exposes a component for every NodeType (compile-time guaranteed by Record)", () => {
    const types: NodeType[] = [
      "classify",
      "branch",
      "switch",
      "http",
      "db_query",
      "shell",
      "go_script",
      "python",
      "transform",
      "end",
      "session_init",
      "agent",
      "connector",
      "channel",
      "parallel",
      "merge",
      "datatable_get",
      "datatable_exists",
      "datatable_query",
      "datatable_insert",
      "datatable_upsert",
      "datatable_delete",
      "datatable_count",
    ];
    for (const t of types) {
      expect(nodeRegistry[t], `missing component for ${t}`).toBeTruthy();
    }
  });

  it("componentFor returns EndNode fallback for an unknown type", () => {
    // Casting to bypass the type guard — replicating runtime drift where
    // the server might emit a type the FE hasn't shipped yet.
    const fallback = componentFor("invented_type" as unknown as NodeType);
    expect(fallback).toBe(nodeRegistry["end"]);
  });

  it("datatable variants share the same component", () => {
    expect(nodeRegistry["datatable_get"]).toBe(nodeRegistry["datatable_exists"]);
    expect(nodeRegistry["datatable_get"]).toBe(nodeRegistry["datatable_insert"]);
  });
});
