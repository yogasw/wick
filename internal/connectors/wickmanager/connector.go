// Package wickmanager exposes wick's own management plane (apps, jobs,
// tools, connectors, lifecycle server/worker) as a fixed single-
// instance connector. The MCP layer expands this connector's ops into
// top-level tools named wick_manager_<area>_<verb> (see
// internal/mcp/handler.go for the expansion); the connector framework
// gives wickmanager admin UI, tag visibility, and run history for free.
//
// Why a connector and not a bespoke MCP surface: every other wick
// connector already gets discovery (wick_list / wick_get / wick_search),
// per-instance admin pages, tag-based access control, encrypted-fields
// support, and connector_runs audit. Reusing the contract here avoids
// a parallel MCP code path.
//
// File layout follows the standard wick connector split:
//
//   - connector.go — Meta, Configs, per-op Input structs, Operations,
//     and the thin handler wrappers (this file).
//   - service.go   — Deps struct + glue calling existing wick services.
//   - access.go    — gate helpers (requireAdmin / requireJobAccess /
//     requireTray / per-resource visibility filters).
//   - audit.go     — defer-elapsed logger that emits to mcp.log via
//     processctl.MCPLogger().
//
// Single source of truth for who-can-call-what is the "Akses control —
// full per-op rule" table in internal/docs/plan_wickmanager.md. Op
// handlers below MUST mirror that table — adding a new op without the
// matching gate helper is a security hole.
package wickmanager

import "github.com/yogasw/wick/pkg/connector"

// Key is the connector definition slug. Single instance is auto-seeded
// on first boot by Service.Bootstrap (Meta.Fixed=true).
const Key = "wickmanager"

// Configs is intentionally empty — wickmanager talks to in-process
// services, not an external API. Kept as an explicit struct so the
// admin form renders "no config required" rather than nothing.
type Configs struct{}

// Meta returns the static metadata block app.RegisterWickManagerConnector
// hands to the registry. Fixed=true so wick auto-seeds exactly one row
// and the manager UI hides "+ New row" / Duplicate / Delete.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Wick Manager",
		Description: "Read and edit wick's own apps / jobs / tools / connectors / process lifecycle. Use this only when the user asks about wick itself, not third-party APIs.",
		Icon:        "🛠",
		Fixed:       true,
	}
}

// Module returns the fully-wired connector.Module for the given deps.
// Registers no global state — caller is responsible for handing the
// result to connectors.Register before connectors.Service.Bootstrap.
func Module(deps Deps) connector.Module {
	return connector.Module{
		Meta:       Meta(),
		Operations: Operations(deps),
	}
}

// Operations builds the closure-bound op list for the registry. All
// handlers capture `deps` so each op can reach into wick services
// without a global accessor (Opsi C of the plan).
func Operations(deps Deps) []connector.Operation {
	h := newHandlers(deps)
	return []connector.Operation{
		// app_*
		connector.Op("app_list", "List App Variables",
			"List app-level configuration variables (session secret, app URL, encryption key, etc). Returns array of {key, type, description, is_secret, is_set, is_locked, can_regenerate, value}. Secret values are masked. Access: ADMIN ONLY. UI: <app_url>/admin/variables.",
			emptyInput{}, h.appList),
		connector.Op("app_get_config", "Get App Variable",
			"Get one app-level config row by key. Returns {key, type, description, is_secret, is_set, value} (value masked if secret). Access: ADMIN ONLY. UI: <app_url>/admin/variables.",
			appGetInput{}, h.appGetConfig),
		connector.Op("app_set_config", "Set App Variable",
			"Update one app-level config value. Rejects rows where is_locked=true. Validates Required field is non-empty. Returns {ok: true, key, before, after} (both masked if secret). Access: ADMIN ONLY. UI: <app_url>/admin/variables.",
			appSetInput{}, h.appSetConfig),
		connector.OpDestructive("app_regenerate_config", "Regenerate App Variable",
			"Regenerate the value of a regenerate-able app config (e.g. session_secret). High-impact — regenerating session_secret logs out other admins. Returns {ok: true, key, regenerated_at}. Access: ADMIN ONLY. UI: <app_url>/admin/variables.",
			appRegenerateInput{}, h.appRegenerateConfig),

		// job_*
		connector.Op("job_list", "List Jobs",
			"List background jobs visible to the caller. Tag-filtered: admin sees all, non-admin sees only jobs their tags grant access to. Returns array of {key, name, description, icon, schedule, enabled, last_status, last_run_at, total_runs, max_runs, has_config}. UI: <app_url>/admin/jobs (admin) or <app_url>/manager/jobs/{key} (per-job).",
			emptyInput{}, h.jobList),
		connector.Op("job_get", "Get Job",
			"Get one job's full detail — meta + configs. Returns {meta, configs: [...]}. Secret config values masked. Access: per-job-access. UI: <app_url>/manager/jobs/{key}.",
			jobKeyInput{}, h.jobGet),
		connector.Op("job_set_config", "Set Job Config",
			"Update one of a job's config values. Rejects rows where is_locked=true. Returns {ok: true, key, config_key, before, after} (masked if secret). Access: per-job-access. NOTE: UI dashboard restricts edit to admin; MCP is more permissive — caller with tag access can edit here. UI: <app_url>/manager/jobs/{key}.",
			jobSetConfigInput{}, h.jobSetConfig),
		connector.Op("job_set_schedule", "Set Job Schedule",
			"Update a job's cron schedule and toggle enabled/max_runs cap. schedule is standard 5-field cron expression. Returns {ok: true, key, schedule, enabled, max_runs}. Access: per-job-access. UI: <app_url>/manager/jobs/{key}.",
			jobSetScheduleInput{}, h.jobSetSchedule),
		connector.Op("job_run_now", "Run Job Now",
			"Trigger an out-of-cycle run of the named job. Returns immediately with the run id; run executes in background. Errors if job is already running or max_runs reached. Returns {run_id, status: started, started_at}. Access: per-job-access. UI: <app_url>/manager/jobs/{key}.",
			jobKeyInput{}, h.jobRunNow),
		connector.Op("job_get_run", "Get Job Run",
			"Get one job run's status + result. Caller must have tag access to the parent job. Returns {id, job_key, status, result, triggered_by, started_at, ended_at}. Access: per-job-access. UI: <app_url>/manager/jobs/{key}.",
			jobGetRunInput{}, h.jobGetRun),
		connector.Op("job_list_runs", "List Job Runs",
			"List recent runs of a job, newest first. Returns array of {id, status, triggered_by, started_at, ended_at, duration_ms}. Access: per-job-access. UI: <app_url>/manager/jobs/{key}.",
			jobListRunsInput{}, h.jobListRuns),

		// tool_*
		connector.Op("tool_list", "List Tools",
			"List tools (UI modules) visible to the caller. Tag-filtered. Returns array of {key, name, description, icon, category, has_config}. UI: <app_url>/admin/tools (admin) or <app_url>/manager/tools/{key}.",
			emptyInput{}, h.toolList),
		connector.Op("tool_get", "Get Tool",
			"Get one tool's full detail — meta + configs. Returns {meta, configs: [...]} (secret masked). Access: per-tool-access. UI: <app_url>/manager/tools/{key}.",
			toolKeyInput{}, h.toolGet),
		connector.Op("tool_set_config", "Set Tool Config",
			"Update one of a tool's config values. Rejects locked rows. Returns {ok: true, key, config_key, before, after} (masked if secret). Access: per-tool-access. MCP-permissive vs UI (UI: admin-only). UI: <app_url>/manager/tools/{key}.",
			toolSetConfigInput{}, h.toolSetConfig),

		// connector_*
		connector.Op("connector_list", "List Connectors",
			"List connector instances visible to the caller. Tag-filtered. Returns array of {id, key, label, description, icon, status, total_tools, disabled, has_config}. status is ready (all required configs filled) or needs_setup. UI: <app_url>/admin/connectors (admin) or <app_url>/manager/connectors/{id}.",
			emptyInput{}, h.connectorList),
		connector.Op("connector_get", "Get Connector",
			"Get one connector's full detail — meta + configs + operations. Returns {meta, configs: [...], operations: [{key, name, description, destructive}]}. Secret config masked. Access: per-connector-access. UI: <app_url>/manager/connectors/{id}.",
			connectorIDInput{}, h.connectorGet),
		connector.Op("connector_set_config", "Set Connector Config",
			"Update one of a connector's config values. Rejects locked rows. Returns {ok: true, id, config_key, before, after} (masked if secret). Access: per-connector-access. MCP-permissive vs UI (UI: admin-only). UI: <app_url>/manager/connectors/{id}.",
			connectorSetConfigInput{}, h.connectorSetConfig),

		// system_* (tray-only + admin)
		connector.Op("system_status", "System Status",
			"Get HTTP server + background worker process status. Only available when wick is launched via the system tray. Returns {server_running, server_port, worker_running, run_mode}. Access: ADMIN + tray-only.",
			emptyInput{}, h.systemStatus),
		connector.Op("system_server_start", "Start Server",
			"Start the HTTP server in this tray process. Errors if already running or port in use. Returns {ok: true, port}. Access: ADMIN + tray-only.",
			emptyInput{}, h.systemServerStart),
		connector.OpDestructive("system_server_stop", "Stop Server",
			"Stop the HTTP server. Returns {ok: true}. Access: ADMIN + tray-only.",
			emptyInput{}, h.systemServerStop),
		connector.Op("system_worker_start", "Start Worker",
			"Start the background worker. Errors if already running. Returns {ok: true}. Access: ADMIN + tray-only.",
			emptyInput{}, h.systemWorkerStart),
		connector.OpDestructive("system_worker_stop", "Stop Worker",
			"Stop the background worker. Returns {ok: true}. Access: ADMIN + tray-only.",
			emptyInput{}, h.systemWorkerStop),
		connector.Op("system_prefs_get", "Get Tray Preferences",
			"Read per-machine tray preferences from ~/.<appName>/config.json. Returns {auto_start_app, auto_start_server, auto_start_worker, auto_update, port, log_retention_days, database_path}. Access: ADMIN + tray-only.",
			emptyInput{}, h.systemPrefsGet),
		connector.Op("system_prefs_set", "Set Tray Preferences",
			"Update per-machine tray preferences. PATCH-style merge — only fields present in input are updated; omitted fields keep current value. Returns the new full config. Access: ADMIN + tray-only.",
			systemPrefsSetInput{}, h.systemPrefsSet),
	}
}

// emptyInput marks an op that takes no arguments. Reflected into an
// empty Configs slice; the LLM sees `{}` as the input schema.
type emptyInput struct{}

type appGetInput struct {
	Key string `wick:"required;desc=Variable key (e.g. app_name, session_secret)."`
}

type appSetInput struct {
	Key   string `wick:"required;desc=Variable key."`
	Value string `wick:"textarea;desc=New value. Empty string clears non-secret rows; empty submit on a secret row is a no-op."`
}

type appRegenerateInput struct {
	Key string `wick:"required;desc=Variable key. Must have can_regenerate=true."`
}

type jobKeyInput struct {
	Key string `wick:"required;desc=Job key (Meta.Key from code registration)."`
}

type jobSetConfigInput struct {
	Key       string `wick:"required;desc=Job key."`
	ConfigKey string `wick:"required;desc=Config field key declared on the job's Config struct."`
	Value     string `wick:"textarea;desc=New value. Empty submit on a secret field keeps the stored value."`
}

type jobSetScheduleInput struct {
	Key      string `wick:"required;desc=Job key."`
	Schedule string `wick:"desc=Standard 5-field cron expression (e.g. */30 * * * *). Empty disables scheduled runs but keeps Run Now available."`
	Enabled  bool   `wick:"desc=When true, the scheduler picks up the cron expression on the next tick."`
	MaxRuns  int    `wick:"number;desc=Cap on total runs (lifetime). 0 = unlimited."`
}

type jobGetRunInput struct {
	RunID string `wick:"required;desc=Run id returned by job_run_now or job_list_runs."`
}

type jobListRunsInput struct {
	Key   string `wick:"required;desc=Job key."`
	Limit int    `wick:"number;desc=Max rows to return. 0 = use default 20."`
}

type toolKeyInput struct {
	Key string `wick:"required;desc=Tool key (Meta.Key from code registration)."`
}

type toolSetConfigInput struct {
	Key       string `wick:"required;desc=Tool key."`
	ConfigKey string `wick:"required;desc=Config field key."`
	Value     string `wick:"textarea;desc=New value."`
}

type connectorIDInput struct {
	ID string `wick:"required;desc=Connector instance id (uuid). Use connector_list to enumerate."`
}

type connectorSetConfigInput struct {
	ID        string `wick:"required;desc=Connector instance id."`
	ConfigKey string `wick:"required;desc=Config field key declared on the connector's Configs struct."`
	Value     string `wick:"textarea;desc=New value."`
}

type systemPrefsSetInput struct {
	AutoStartApp     *bool   `wick:"desc=Register binary with OS to launch at user login."`
	AutoStartServer  *bool   `wick:"desc=Start HTTP server when tray launches."`
	AutoStartWorker  *bool   `wick:"desc=Start background worker when tray launches."`
	AutoUpdate       *bool   `wick:"desc=Check + download new releases in background."`
	Port             *int    `wick:"number;desc=HTTP listen port. 0 = use default 9425."`
	LogRetentionDays *int    `wick:"number;desc=Days of per-day log files to keep. 0 = default 7."`
	DatabasePath     *string `wick:"desc=Override SQLite DB path. Empty = auto-detect."`
}
