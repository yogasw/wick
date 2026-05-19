// Package workflow exposes the workflow engine as a fixed single-instance
// MCP connector. Every op in §9 of the workflow design (Tier 1/2/3) is
// reachable via wick_execute so any AI with MCP access — Claude Desktop,
// ChatGPT plugin, Gemini — can create, edit, test, and run workflows
// without needing native file access.
//
// File layout:
//
//   - connector.go — Meta, Configs, Input structs, Operations, thin handlers
//   - ops.go       — handler implementations that delegate to mcp.Ops
//
// Wire-up: call workflow.Module(ops) and pass the result to
// connectors.Register(...) before connectors.Service.Bootstrap. The
// mcp.Ops pointer is obtained from the workflow bootstrap
// (internal/agents/workflow/). Use workflow.ModuleWithRunner(ops, runner)
// to also enable the workflow_test / workflow_test_coverage ops.
//
// AI usage pattern (no file access):
//
//	workflow_workspace → workflow_node_types → workflow_create →
//	workflow_add_node  → workflow_connect    → workflow_validate →
//	workflow_simulate  → workflow_test       → workflow_request_review
package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/wftest"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "workflow"

// Configs is intentionally empty — the connector talks to the in-process
// workflow engine, not an external API.
type Configs struct{}

// Meta returns the static metadata block.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Workflow",
		Description: "Create, edit, test, and run wick workflows via MCP. Full Tier 1/2/3 surface: introspect node types, build graphs, write files, validate, simulate, run tests, capture fixtures, and request review.",
		Icon:        "⚙",
		Fixed:       true,
	}
}

// Module returns the fully-wired connector.Module for the given ops.
// Call connectors.Register(workflow.Module(ops)) at boot before
// connectors.Service.Bootstrap runs. The workflow_test / workflow_test_coverage
// ops return an error when no runner is wired — use ModuleWithRunner to enable.
func Module(ops *wfmcp.Ops) connector.Module {
	return ModuleWithRunner(ops, nil)
}

// ModuleWithRunner is like Module but also wires a wftest.Runner so the
// workflow_test and workflow_test_coverage ops are functional.
func ModuleWithRunner(ops *wfmcp.Ops, runner *wftest.Runner) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector}
	return connector.Module{
		Meta:       m,
		Operations: Operations(ops, runner),
	}
}

// Operations builds the full op list, capturing ops + runner so every
// handler can reach the workflow engine.
func Operations(ops *wfmcp.Ops, runner *wftest.Runner) []connector.Operation {
	h := &handlers{ops: ops, runner: runner}
	return []connector.Operation{
		// ── Tier 1: introspection ──────────────────────────────────────
		connector.Op("workflow_workspace", "Workflow Workspace",
			"Entry point. Returns {base_dir, node_types[], trigger_types[], templates[]}. Call this first to orient yourself before creating or editing workflows.",
			emptyInput{}, h.workspace, wickdocs.Docs{}),
		connector.Op("workflow_node_types", "List Node Types",
			"List all node types with schema, example YAML, and when_to_use. Use to know what types are available before calling workflow_add_node.",
			emptyInput{}, h.nodeTypes, wickdocs.Docs{}),
		connector.Op("workflow_trigger_types", "List Trigger Types",
			"List all trigger types with schema + example. Use to know valid trigger shapes before calling workflow_set_triggers.",
			emptyInput{}, h.triggerTypes, wickdocs.Docs{}),
		connector.Op("workflow_channels", "List Channels",
			"List configured channel integrations and their trigger + action schemas. Use to discover what channel ops are available for type:channel nodes.",
			emptyInput{}, h.channels, wickdocs.Docs{}),
		connector.Op("workflow_integration", "List Channel Integration Descriptors",
			"Returns the FULL per-channel event + action catalog from the integration registry — incl. each event's match_schema (filter fields like channel_id whitelist, action_id, text_contains), payload_schema, and each action's input_schema / output_schema / destructive flag. More complete than workflow_channels (which uses legacy specs). Use to know exact filter shapes for trigger.match and exact arg shapes for type:channel nodes.",
			emptyInput{}, h.integration, wickdocs.Docs{}),
		connector.Op("workflow_node_detail", "Workflow Node Detail",
			"Return the full self-documenting detail for one node_type — built-in node (agent, branch, http, ...), channel event/action (channel:slack.message, channel:slack.send_message), connector op (connector:slack.chat_postMessage), or trigger type (trigger:cron, trigger:channel). Returns schema, when_to_use, examples, quirks, templateable_fields, pair_with, common_pitfalls when the source descriptor populated them. Use after workflow_node_types / workflow_workspace listing to fetch deep context for one specific type before adding it to a workflow.",
			nodeDetailInput{}, h.nodeDetail, wickdocs.Docs{}),
		connector.Op("workflow_connectors", "List Connector Ops",
			"List all connector modules and their operations. Use to discover valid (module, op) pairs for type:connector nodes.",
			emptyInput{}, h.connectors, wickdocs.Docs{}),
		connector.Op("workflow_skills", "List Skills",
			"List AI provider skills available for type:agent nodes. Optional filter: {provider}.",
			skillsInput{}, h.skills, wickdocs.Docs{}),
		connector.Op("workflow_providers", "List Providers",
			"List configured AI providers (claude/codex/gemini) with capabilities and status.",
			emptyInput{}, h.providers, wickdocs.Docs{}),
		connector.Op("workflow_list", "List Workflows",
			"List all workflows with id, name, enabled, version. Optional filter by name substring.",
			listInput{}, h.list, wickdocs.Docs{}),
		connector.Op("workflow_check_name", "Check Workflow Name",
			"Check if a workflow Name is already taken. Returns {available, conflict_id}. Call this before workflow_create so AI surfaces a friendly error instead of letting Create reject. Same check the UI form uses.",
			checkNameInput{}, h.checkName, wickdocs.Docs{}),
		connector.Op("workflow_get", "Get Workflow",
			"Get full workflow definition: triggers, graph nodes/edges, env schema. Pass the workflow ID.",
			idInput{}, h.get, wickdocs.Docs{}),
		connector.Op("workflow_list_files", "List Workflow Files",
			"List all files in a workflow folder (workflow.yaml, nodes/*.md, __tests__/, etc.).",
			idInput{}, h.listFiles, wickdocs.Docs{}),
		connector.Op("workflow_read_file", "Read Workflow File",
			"Read content of one file in a workflow folder. Replaces native file tool for remote AI.",
			readFileInput{}, h.readFile, wickdocs.

				// ── Tier 2: write ──────────────────────────────────────────────
				Docs{}),

		connector.Op("workflow_create", "Create Workflow",
			"Scaffold a new workflow folder with a template. Templates: empty, support-triage, incident-response, daily-digest. Returns {id, name}. Newly created workflows start disabled — admin must enable.",
			createInput{}, h.create, wickdocs.Docs{}),
		connector.Op("workflow_write_file", "Write Workflow File",
			"Atomically write a file inside a workflow folder. Safe path — rejects '..' and symlinks. Use for workflow.yaml, nodes/prompt.md, scripts, test fixtures. When writing workflow.yaml: every trigger must have entry_node set to the graph node it starts from (e.g. entry_node: classify). Omitting entry_node disconnects the trigger from the graph.\n\nMatch filter format in YAML: picker fields use native YAML array of {id, name} objects — NOT JSON strings. Example:\n  match:\n    mode: whitelist\n    channel_id:\n      - id: C123\n        name: '#general'\n  match_enabled: true\n\nTemplate refs (preferred): every trigger and node lives under {{.Node.<label>.…}} — payload at {{.Node.<trigger-label>.payload.<key>}}, upstream node fields at {{.Node.<node-label>.<field>}}. Label defaults to node id when no label is set. Legacy {{.Event.*}} still resolves but new workflows should use the Node.<label> form so triggers and nodes share one access pattern.",
			writeFileInput{}, h.writeFile, wickdocs.Docs{}),
		connector.OpDestructive("workflow_delete_file", "Delete Workflow File",
			"Delete a file inside a workflow folder.",
			deleteFileInput{}, h.deleteFile, wickdocs.Docs{}),
		connector.OpDestructive("workflow_delete", "Delete Workflow",
			"Delete the full workflow folder and unregister all scheduled triggers.",
			idInput{}, h.deleteWorkflow, wickdocs.Docs{}),
		connector.Op("workflow_add_node", "Add Node",
			"Add a node to the workflow graph via declarative patch. Validates type + schema. Returns updated workflow.",
			addNodeInput{}, h.addNode, wickdocs.Docs{}),
		connector.Op("workflow_update_node", "Update Node",
			"Merge-patch one node's fields. Use to update prompt, config, on_failure, etc.",
			updateNodeInput{}, h.updateNode, wickdocs.Docs{}),
		connector.OpDestructive("workflow_delete_node", "Delete Node",
			"Remove a node and all edges that reference it.",
			nodeIDInput{}, h.deleteNode, wickdocs.Docs{}),
		connector.Op("workflow_connect", "Connect Nodes",
			"Add an edge between two nodes. Pass case label for classify/branch sources.",
			connectInput{}, h.connect, wickdocs.Docs{}),
		connector.Op("workflow_disconnect", "Disconnect Nodes",
			"Remove an edge between two nodes.",
			disconnectInput{}, h.disconnect, wickdocs.Docs{}),
		connector.Op("workflow_move_node", "Move Node",
			"Update canvas position for a node (x, y pixels). Does not affect execution.",
			moveNodeInput{}, h.moveNode, wickdocs.Docs{}),
		connector.Op("workflow_set_triggers", "Set Triggers",
			"Replace the entire triggers list. Use workflow_get first to read current triggers before replacing. IMPORTANT: every trigger must include entry_node pointing to the graph node it should start from — omitting it disconnects the trigger from the graph.\n\nTrigger JSON uses Go PascalCase field names. match filter format: picker fields (channel_id, user) use [{\"id\":\"C123\",\"name\":\"#ch\"}] array — NOT plain string arrays. mode field controls filtering: \"all\"=no filter, \"whitelist\"=apply picker lists. match_enabled must be true for filters to apply. Example: {\"Type\":\"channel\",\"ChannelName\":\"slack\",\"Event\":\"message\",\"EntryNode\":\"start\",\"MatchEnabled\":true,\"Match\":{\"mode\":\"whitelist\",\"channel_id\":[{\"id\":\"C123\",\"name\":\"#general\"}]}}",
			setTriggersInput{}, h.setTriggers, wickdocs.Docs{}),
		connector.Op("workflow_toggle", "Toggle Workflow",
			"Enable or disable a workflow. Disabled workflows skip cron/channel/webhook but can still be run via workflow_run_now.",
			toggleInput{}, h.toggle, wickdocs.Docs{}),
		connector.Op("workflow_publish", "Publish Draft",
			"Promote workflow.draft.yaml → workflow.yaml and re-register the workflow with the router. Required after any edit (workflow_write_file workflow.yaml, workflow_add_node, workflow_connect, etc.) — edits land in draft until you publish. ALWAYS ask the user before publishing edits.",
			publishInput{}, h.publish, wickdocs.Docs{}),
		connector.Op("workflow_discard_draft", "Discard Draft",
			"Throw away workflow.draft.yaml and revert to the published version.",
			idInput{}, h.discardDraft, wickdocs.Docs{}),
		connector.Op("workflow_has_draft", "Has Draft",
			"Returns {has_draft: bool} — true when there are unpublished edits.",
			idInput{}, h.hasDraft, wickdocs.

				// ── Tier 3: action ─────────────────────────────────────────────
				Docs{}),

		connector.Op("workflow_validate", "Validate Workflow",
			"Parse + validate a workflow: cycle detect, schema check, guard dry-run. Returns {ok, errors[], warnings[]}. Call before simulate or run.",
			idInput{}, h.validate, wickdocs.Docs{}),
		connector.Op("workflow_simulate", "Simulate Workflow",
			"Dry-run a workflow with a synthetic event. No state persisted, no external calls. Returns per-node outputs + path_taken + final_result. Pass event as JSON string.",
			simulateInput{}, h.simulate, wickdocs.Docs{}),
		connector.Op("workflow_test", "Run Tests",
			"Run __tests__/ fixture suite. Optional filter by node ID or test name prefix. Returns [{case, pass, error, diff}] + coverage summary.",
			testInput{}, h.runTests, wickdocs.Docs{}),
		connector.Op("workflow_test_coverage", "Test Coverage",
			"Return which nodes were hit and which are untested across all __tests__/ cases.",
			idInput{}, h.testCoverage, wickdocs.Docs{}),
		connector.Op("workflow_record_test", "Record Test from Run",
			"Generate a __tests__/ fixture by capturing a real run's event + per-node outputs. Returns the fixture YAML path.",
			recordTestInput{}, h.recordTest, wickdocs.Docs{}),
		connector.Op("workflow_capture_fixture", "Capture Node Fixture",
			"Snapshot one node's output from a run as a unit test fixture in __tests__/nodes/.",
			captureFixtureInput{}, h.captureFixture, wickdocs.Docs{}),
		connector.Op("workflow_run_now", "Run Now",
			"Enqueue a manual run (bypasses Enabled check). Optional event payload as JSON. Returns {run_id}.",
			runNowInput{}, h.runNow, wickdocs.Docs{}),
		connector.Op("workflow_get_runs", "Get Runs",
			"List recent run IDs + status + started_at for a workflow. Default limit 20.",
			getRunsInput{}, h.getRuns, wickdocs.Docs{}),
		connector.Op("workflow_get_run", "Get Run",
			"Get full run state: node outputs, events, path_taken, status, cost.",
			getRunInput{}, h.getRun, wickdocs.Docs{}),
		connector.Op("workflow_get_run_events", "Get Run Events",
			"Get the raw events.jsonl stream for a run: every node_started / node_completed / node_failed / edge_traversed entry with timestamps and data payloads. Use this when workflow_get_run doesn't have enough detail — e.g. user gives you a failed run ID and asks why it broke.",
			getRunInput{}, h.getRunEvents, wickdocs.Docs{}),
		connector.Op("workflow_get_run_log", "Get Run Log",
			"Combined debug view of a run: status + error + completed/failed/skipped nodes + per-node duration + total duration. One-shot summary for 'why did run X fail'.",
			getRunInput{}, h.getRunLog, wickdocs.Docs{}),
		connector.Op("workflow_copy_run_to_editor", "Copy Run to Editor",
			"UI parity with 'Copy to editor' button. Loads run state, saves current published workflow as draft (for editing), and writes runs/<run_id>/mocks.json with the run's per-node outputs so Execute step can prefill from real data. Use when user says 'tadi run X gagal, kasih edit' — caller still needs to ask user before workflow_publish.",
			copyRunInput{}, h.copyRunToEditor, wickdocs.Docs{}),
		connector.Op("workflow_replay_run", "Replay Run",
			"Re-enqueue a run with the same trigger event as a past run. Convenience wrapper: loads RunState.Event from run_id and calls run_now. Returns new run_id.",
			getRunInput{}, h.replayRun, wickdocs.Docs{}),
		connector.Op("workflow_list_test_cases", "List Test Cases",
			"List __tests__/*.json fixtures with name + assertion count + last result if available. Same source UI Tests tab reads.",
			idInput{}, h.listTestCases, wickdocs.Docs{}),
		connector.Op("workflow_save_test_case", "Save Test Case",
			"Create or update one __tests__/<name>.json fixture. Mirrors the '+ New' modal in the UI Tests tab. Slug-safe name only (a-z 0-9 dash underscore).",
			saveTestCaseInput{}, h.saveTestCase, wickdocs.Docs{}),
		connector.Op("workflow_delete_test_case", "Delete Test Case",
			"Delete one __tests__/<name>.json fixture.",
			deleteTestCaseInput{}, h.deleteTestCase, wickdocs.Docs{}),
		connector.Op("workflow_request_review", "Request Review",
			"Notify admin that the workflow is ready for approval. Workflow stays disabled until admin enables it. Returns {url}.",
			requestReviewInput{}, h.requestReview, wickdocs.Docs{

				// ── Input structs ──────────────────────────────────────────────────────
			}),
	}
}

type emptyInput struct{}

type idInput struct {
	ID string `wick:"required;desc=Workflow ID (folder name)."`
}

type nodeDetailInput struct {
	NodeType string `wick:"required;desc=Node type key. Built-in: agent, branch, http, etc. Channel: channel:slack.message, channel:slack.send_message. Connector: connector:slack.chat_postMessage. Trigger: trigger:cron, trigger:channel."`
}

type listInput struct {
	Filter string `wick:"desc=Optional name substring filter."`
}

type checkNameInput struct {
	Name     string `wick:"required;desc=Workflow display name to check."`
	ExceptID string `wick:"desc=Optional workflow ID to ignore (for editing existing workflow without flagging itself)."`
}

type skillsInput struct {
	Provider string `wick:"desc=Provider name (claude/codex/gemini). Omit to list all."`
}

type readFileInput struct {
	ID   string `wick:"required;desc=Workflow ID."`
	Path string `wick:"required;desc=Relative file path inside workflow folder. Example: workflow.yaml or nodes/prompt.md"`
}

type createInput struct {
	Name     string `wick:"required;desc=Display name for the workflow."`
	Template string `wick:"desc=Starter template: empty (default), support-triage, incident-response, daily-digest."`
}

type writeFileInput struct {
	ID      string `wick:"required;desc=Workflow ID."`
	Path    string `wick:"required;desc=Relative path inside workflow folder. Example: workflow.yaml"`
	Content string `wick:"textarea;required;desc=File content (full replace — not a patch)."`
}

type deleteFileInput struct {
	ID   string `wick:"required;desc=Workflow ID."`
	Path string `wick:"required;desc=Relative file path to delete."`
}

type addNodeInput struct {
	ID   string `wick:"required;desc=Workflow ID."`
	Node string `wick:"textarea;required;desc=Node definition as JSON. Must include id and type. See workflow_node_types for schemas."`
}

type updateNodeInput struct {
	ID     string `wick:"required;desc=Workflow ID."`
	NodeID string `wick:"required;desc=Node ID to update."`
	Patch  string `wick:"textarea;required;desc=Fields to merge as JSON object. Existing fields not present are unchanged."`
}

type nodeIDInput struct {
	ID     string `wick:"required;desc=Workflow ID."`
	NodeID string `wick:"required;desc=Node ID to remove."`
}

type connectInput struct {
	ID        string `wick:"required;desc=Workflow ID."`
	FromID    string `wick:"required;desc=Source node ID."`
	ToID      string `wick:"required;desc=Target node ID."`
	CaseLabel string `wick:"desc=Case label for classify/branch sources (e.g. bug, default). Omit for unconditional edges."`
}

type disconnectInput struct {
	ID     string `wick:"required;desc=Workflow ID."`
	FromID string `wick:"required;desc=Source node ID."`
	ToID   string `wick:"required;desc=Target node ID."`
}

type moveNodeInput struct {
	ID     string `wick:"required;desc=Workflow ID."`
	NodeID string `wick:"required;desc=Node ID."`
	X      int    `wick:"required;desc=Canvas X position in pixels."`
	Y      int    `wick:"required;desc=Canvas Y position in pixels."`
}

type setTriggersInput struct {
	ID       string `wick:"required;desc=Workflow ID."`
	Triggers string `wick:"textarea;required;desc=Triggers list as JSON array. Each item must have type and entry_node (the graph node ID the trigger starts from). Use workflow_trigger_types to see schemas."`
}

type toggleInput struct {
	ID      string `wick:"required;desc=Workflow ID."`
	Enabled bool   `wick:"desc=true to enable, false to disable."`
}

type simulateInput struct {
	ID    string `wick:"required;desc=Workflow ID."`
	Event string `wick:"textarea;required;desc=Synthetic trigger event as JSON. Minimum: {\"Type\":\"manual\"}. Full shape: {\"Type\":\"channel\",\"Text\":\"...\",\"Channel\":\"C123\",\"User\":\"U999\"}."`
}

type testInput struct {
	ID     string `wick:"required;desc=Workflow ID."`
	Filter string `wick:"desc=Optional filter prefix: 'node:classify-intent' for unit tests, 'integration:' for integration tests, or free text for test name match."`
}

type recordTestInput struct {
	ID    string `wick:"required;desc=Workflow ID."`
	RunID string `wick:"required;desc=Run ID to capture. Use workflow_get_runs to find recent run IDs."`
}

type captureFixtureInput struct {
	ID     string `wick:"required;desc=Workflow ID."`
	RunID  string `wick:"required;desc=Run ID."`
	NodeID string `wick:"required;desc=Node ID whose output to snapshot as a unit test fixture."`
}

type runNowInput struct {
	ID    string `wick:"required;desc=Workflow ID."`
	Event string `wick:"textarea;desc=Optional trigger event as JSON. Defaults to {\"Type\":\"manual\"} when empty."`
}

type getRunsInput struct {
	ID    string `wick:"required;desc=Workflow ID."`
	Limit int    `wick:"desc=Max runs to return. Default 20."`
}

type getRunInput struct {
	ID    string `wick:"required;desc=Workflow ID."`
	RunID string `wick:"required;desc=Run ID from workflow_get_runs."`
}

type requestReviewInput struct {
	ID      string `wick:"required;desc=Workflow ID."`
	Message string `wick:"textarea;desc=Optional summary message for the reviewer."`
}

type publishInput struct {
	ID     string `wick:"required;desc=Workflow ID."`
	Enable bool   `wick:"desc=When true (default), also enable the workflow so triggers fire immediately."`
}

type copyRunInput struct {
	ID    string `wick:"required;desc=Workflow ID."`
	RunID string `wick:"required;desc=Run ID to copy into the editor."`
}

type saveTestCaseInput struct {
	ID         string `wick:"required;desc=Workflow ID."`
	Name       string `wick:"required;desc=Test case name. Slug-safe: a-z 0-9 dash underscore."`
	Input      string `wick:"textarea;required;desc=Input as JSON: {Event:{Type,Payload}, Node:{...optional upstream outputs}}. Example: {\"Event\":{\"Type\":\"manual\",\"Payload\":{\"text\":\"bug di checkout\"}}}"`
	Assertions string `wick:"textarea;desc=Optional assertions as JSON array: [{subject:status,operator:==,value:success}, ...]. Operators: ==, !=, contains, case_fired, node_skipped, path_taken, edge_traversed."`
}

type deleteTestCaseInput struct {
	ID   string `wick:"required;desc=Workflow ID."`
	Name string `wick:"required;desc=Test case name (without .json)."`
}

// ── Handler struct ────────────────────────────────────────────────────

type handlers struct {
	ops    *wfmcp.Ops
	runner *wftest.Runner
}

// parseJSON is a small helper that decodes a JSON string from a connector
// Input field into the target type. Returns a clear error on bad JSON.
func parseJSON[T any](raw string, target *T) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("expected JSON, got empty string")
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

// ctxFrom extracts or creates a background context from the connector Ctx.
// connector.Ctx does not carry an http.Request context in the normal
// (non-HTTP) dispatch path, so we always use Background here and rely on
// the engine's own timeout budget.
func ctxFrom(_ *connector.Ctx) context.Context {
	return context.Background()
}

// ok is a shorthand response for write ops that succeed without a
// meaningful return value.
func ok(msg string) map[string]any {
	return map[string]any{"ok": true, "message": msg}
}
