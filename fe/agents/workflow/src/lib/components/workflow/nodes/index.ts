// Registry: map NodeType → Svelte component. Adding a node type =
// drop a `.svelte` next to this file + one line here. Mirror of the Go
// `internal/tools/agents/workflow/nodes/*` registry pattern.
import type { Component } from "svelte";
import type { NodeType } from "$lib/types/workflow";

import ClassifyNode from "./ClassifyNode.svelte";
import BranchNode from "./BranchNode.svelte";
import SwitchNode from "./SwitchNode.svelte";
import HttpNode from "./HttpNode.svelte";
import DbQueryNode from "./DbQueryNode.svelte";
import ShellNode from "./ShellNode.svelte";
import GoScriptNode from "./GoScriptNode.svelte";
import PythonNode from "./PythonNode.svelte";
import TransformNode from "./TransformNode.svelte";
import EndNode from "./EndNode.svelte";
import SessionInitNode from "./SessionInitNode.svelte";
import AgentNode from "./AgentNode.svelte";
import ConnectorNode from "./ConnectorNode.svelte";
import ChannelNode from "./ChannelNode.svelte";
import DatatableNode from "./DatatableNode.svelte";
import ParallelNode from "./ParallelNode.svelte";
import MergeNode from "./MergeNode.svelte";

export const nodeRegistry: Record<NodeType, Component> = {
  classify: ClassifyNode,
  branch: BranchNode,
  switch: SwitchNode,
  http: HttpNode,
  db_query: DbQueryNode,
  shell: ShellNode,
  go_script: GoScriptNode,
  python: PythonNode,
  transform: TransformNode,
  end: EndNode,
  session_init: SessionInitNode,
  agent: AgentNode,
  connector: ConnectorNode,
  channel: ChannelNode,
  parallel: ParallelNode,
  merge: MergeNode,
  datatable_get: DatatableNode,
  datatable_exists: DatatableNode,
  datatable_query: DatatableNode,
  datatable_insert: DatatableNode,
  datatable_upsert: DatatableNode,
  datatable_delete: DatatableNode,
  datatable_count: DatatableNode,
};

export function componentFor(t: NodeType): Component {
  return nodeRegistry[t] ?? EndNode;
}
