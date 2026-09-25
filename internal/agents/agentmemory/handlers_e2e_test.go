package agentmemory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

// End-to-end over the dashboard's own surface.
//
// The unit tests next door check one handler's decision at a time. What they
// cannot catch is the failure this file exists for: a Go struct that renames,
// drops or adds a JSON field still compiles, still passes every Go test, and
// silently blanks a card in the panel — the FE reads `page_count` and gets
// `pages`, renders nothing, and reports no error anywhere.
//
// So every endpoint is driven through the REAL route registration, as a real
// role, against a backend that answers realistic data, and the RESPONSE SHAPE
// is asserted field by field against fe/agents/agentmemory/src/lib/types.ts.
// The lists in `shape` below are that file, transcribed: a name here that the
// FE does not declare is as much a failure as a name the FE declares and the
// server does not send.
//
// Not covered in-process, and why: this host has no ai-memory binary, so
// nothing here talks to a daemon. That is deliberately used rather than
// worked around — the daemon-down path is the real state of this host, and
// the test asserts the panel still gets a complete answer in it.

const e2eID = "e2e-mem"

// ── the backend under test ───────────────────────────────────────────

// e2eData answers like a store that has been in use: every optional field is
// populated, because a payload of zeros cannot show that an omitempty field
// still serialises under the name the FE reads.
type e2eData struct{}

func (e2eData) StoreStatus(context.Context, Conn) (*StoreStatus, error) {
	return &StoreStatus{
		Version:     "2.4.0",
		DataDir:     "/srv/agent-memory/data",
		DBPath:      "/srv/agent-memory/data/memory.db",
		Bind:        "127.0.0.1:49374",
		CaptureMode: "denylist",
		Counts:      StoreCounts{PagesLatest: 412, PagesAll: 908, Sessions: 77, Observations: 5310},
		Index: IndexState{
			PagesRows: 908, PagesFTSRows: 908,
			ObservationsRows: 5310, ObservationsFTSRows: 5310,
			EmbeddingRows: 402, LatestPagesMissingEmbed: 10,
		},
		Storage: StorageState{DatabaseBytes: 41_943_040, ReclaimableBytes: 2_097_152, FreeBytes: 9_663_676_416},
		Ingest: IngestState{
			Accepted: 5310, DroppedByPolicy: 12, ShedSaturated: 1, ShedRateLimited: 3,
			LastPersistedMS: 1_790_000_000_000,
		},
		Spool:     SpoolState{Pending: 4, OldestAgeMS: 900_000, RetriesTotal: 7},
		LLM:       ProviderState{Status: "ready", Provider: "ollama", Model: "qwen2.5:7b", LastErr: "context length exceeded once"},
		Embedding: ProviderState{Status: "ready", Provider: "ollama", Model: "nomic-embed-text", Dim: 768},
		Raw:       json.RawMessage(`{"schema":"2"}`),
	}, nil
}

func (e2eData) Projects(context.Context, Conn) ([]ProjectRow, error) {
	return []ProjectRow{
		{Workspace: "wick", Project: "kasir-8c28230d", PageCount: 31, LastUpdated: "2026-09-25T10:00:00Z"},
		{Workspace: "wick", Project: "brand-1f2e3d4c", PageCount: 4, LastUpdated: "2026-09-24T09:30:00Z"},
	}, nil
}

// ProjectBriefing is the one MCP call the dashboard makes, and the Projects
// tab reads every field of it.
func (e2eData) ProjectBriefing(_ context.Context, _ Conn, s ReadScope) (*ProjectBriefing, error) {
	return &ProjectBriefing{
		Counts:            BriefingCounts{PagesLatest: 31, PagesAll: 64, Sessions: 12, Observations: 880, EvidenceRows: 210},
		Activity7d:        ActivityWindow{Days: 7, Sessions: 3, Observations: 120, PagesUpdated: 9},
		Activity30d:       ActivityWindow{Days: 30, Sessions: 12, Observations: 880, PagesUpdated: 31},
		LastObservationAt: "2026-09-25T09:58:00Z",
		PendingHandoffs:   1,
		PendingMessages:   2,
		RecentPages: []RecentPage{
			{Path: "sessions/1c0f.md", Title: "Kasir prod debug", Kind: "session", UpdatedAt: "2026-09-25T09:58:00Z"},
		},
		CrossProjectDependents:   1,
		CrossProjectDependencies: 2,
	}, nil
}

func (e2eData) Health(context.Context, Conn, ReadScope) (*HealthReport, error) {
	return &HealthReport{
		Doctor: DoctorReport{
			Workspace: "wick", Project: "kasir-8c28230d", SinceDays: 7,
			Rows:       json.RawMessage(`[{"agent":"claude","local_total":40,"local_recent":6,"captured":6,"uncaptured":false},{"agent":"codex","local_total":11,"local_recent":3,"captured":0,"uncaptured":true}]`),
			Uncaptured: json.RawMessage(`["codex"]`),
		},
		Contamination: ContaminationReport{
			SessionsMisbucketed: 2,
			Findings:            json.RawMessage(`[{"session":"9f2","expected":"wick/kasir-8c28230d","stored":"default/files"}]`),
		},
	}, nil
}

func (e2eData) Handoffs(context.Context, Conn, ReadScope, int) ([]Handoff, error) {
	return []Handoff{
		{ID: "h-1", FromAgent: "claude", ToAgent: "codex", CWD: "/srv/projects/8c28230d/files", CreatedAtMS: 1_790_000_100_000},
	}, nil
}

func (e2eData) Search(context.Context, Conn, ReadScope, string) ([]SearchHit, error) {
	return []SearchHit{
		{
			Workspace: "wick", Project: "kasir-8c28230d", Path: "pages/kasir_prod_db.md",
			Title: "kasir_prod_db", Kind: "fact", Snippet: "the <mark>kasir_prod_db</mark> replica", Rank: 0.87,
		},
	}, nil
}

func (e2eData) Backfill(_ context.Context, _ Conn, req BackfillRequest) (*BackfillReport, error) {
	return &BackfillReport{
		Workspace: req.Scope.Workspace, Project: req.Scope.Project,
		Selected: 9, ImportedSessions: 7, ImportedEvents: 240,
		SkippedForCap: 2, FailedSessions: 1, SkippedNonEmpty: false, DryRun: req.DryRun,
	}, nil
}

func (e2eData) Compact(context.Context, Conn) (*CompactReport, error) {
	return &CompactReport{Output: "Compacted: 40.0 MiB → 38.0 MiB (2.0 MiB reclaimed)."}, nil
}

func (e2eData) ReadPage(_ context.Context, _ Conn, _ ReadScope, path string) (*Page, error) {
	return &Page{
		Path: path, Workspace: "wick", Project: "kasir-8c28230d", Title: "kasir_prod_db",
		Body:        "# kasir_prod_db\n\nRead replica, not the primary.\n",
		Frontmatter: json.RawMessage(`{"kind":"fact","confidence":0.9}`),
	}, nil
}

func (e2eData) Messages(context.Context, Conn, ReadScope, string, int) ([]Message, error) {
	return []Message{
		{
			ID: "m-1", Subject: "schema change", Body: "the replica lags 2m",
			FromAgent: "codex", FromWorkspaceID: "ws-1", FromProjectID: "p-2",
			State: "pending", CreatedAt: "2026-09-25T08:00:00Z", ClaimedAt: "2026-09-25T08:05:00Z",
		},
	}, nil
}

func (e2eData) CancelHandoff(_ context.Context, _ Conn, _ ReadScope, id string) (*HandoffCancelResult, error) {
	return &HandoffCancelResult{HandoffID: id, Cancelled: true, State: "cancelled"}, nil
}

func (e2eData) ForgetSweep(_ context.Context, _ Conn, _ ReadScope, dryRun bool) (*SweepReport, error) {
	return &SweepReport{DryRun: dryRun, Output: "Would evict 12 cold pages and prune 400 observations."}, nil
}

// The editing half (PLAN §22). Same rule as the reads: every optional field
// is filled, because a payload of zeros cannot show that an omitempty field
// still serialises under the name the panel reads.

func (e2eData) WritePage(_ context.Context, _ Conn, s ReadScope, w PageWrite) (*PageWriteResult, error) {
	return &PageWriteResult{
		Path:   w.Path,
		PageID: "01a0d8dd",
		Output: "✓ wrote " + w.Path + " (page_id=01a0d8dd) under " + s.Workspace + "/" + s.Project,
	}, nil
}

func (e2eData) DeletePage(_ context.Context, _ Conn, s ReadScope, path string) (*PageDeleteResult, error) {
	return &PageDeleteResult{Path: path, Deleted: true, Output: "✓ deleted " + path + " under " + s.Workspace + "/" + s.Project}, nil
}

func (e2eData) Checkpoints(context.Context, Conn, int) ([]Checkpoint, error) {
	return []Checkpoint{{
		OID: "d544bc66fa5e33c4da76b37982e251da373b9a33", ShortOID: "d544bc66fa5e",
		TimeUnix: 1790344784, Summary: "write-page wick/kasir-8c28230d: pages/kasir_prod_db.md",
	}}, nil
}

func (e2eData) RestorePage(_ context.Context, _ Conn, _ ReadScope, path, from string) (*RestoreResult, error) {
	return &RestoreResult{
		PageID: "01a0d8dd", Path: path, RestoredFrom: from,
		PreCheckpoint: "c84640f0a0f5", Checkpoint: "25d150d14afd",
	}, nil
}

// e2eFailingData is the same backend with its reads refusing the way a real
// one does when the daemon's web API is off. It exists because the FE has a
// whole branch for that answer — DataFailure — and a shape nothing exercises
// is a shape nobody has checked.
type e2eFailingData struct{ e2eData }

func (e2eFailingData) StoreStatus(context.Context, Conn) (*StoreStatus, error) {
	return nil, ErrWebDisabled
}

func (e2eFailingData) Projects(context.Context, Conn) ([]ProjectRow, error) {
	return nil, ErrWebDisabled
}

// ── harness ──────────────────────────────────────────────────────────

// shape is one payload's field list as types.ts declares it: `required` are
// the fields the FE reads unconditionally, `optional` the ones it marks `?`.
//
// The assertion runs both ways on purpose. A missing required field is a card
// that renders blank; an EXTRA field is a payload the FE does not know about,
// which is how a rename gets missed — the old name disappears and the new one
// arrives, and only the two-way check sees both halves.
type shape struct {
	required []string
	optional []string
}

func assertShape(t *testing.T, what string, got map[string]any, s shape) {
	t.Helper()
	allowed := map[string]bool{}
	for _, k := range s.required {
		allowed[k] = true
	}
	for _, k := range s.optional {
		allowed[k] = true
	}
	for _, k := range s.required {
		if _, ok := got[k]; !ok {
			t.Errorf("%s: types.ts reads %q and the server did not send it (keys: %v)", what, k, keysOf(got))
		}
	}
	for k := range got {
		if !allowed[k] {
			t.Errorf("%s: server sends %q, which types.ts does not declare — add it there or drop it here", what, k)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sub reads a nested object, failing rather than silently skipping the
// assertions that were meant to run on it.
func sub(t *testing.T, what string, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%s: %q is not an object (got %T)", what, key, m[key])
	}
	return v
}

// list reads a nested array. It fails on `null` as well as on a wrong type:
// a list that arrives as null instead of [] is the difference between "no
// rows" and a `.map is not a function` in the panel.
func list(t *testing.T, what string, m map[string]any, key string) []any {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("%s: %q is missing", what, key)
	}
	if v == nil {
		t.Fatalf("%s: %q serialised as null — lists must be [] so the FE can map over them", what, key)
	}
	rows, ok := v.([]any)
	if !ok {
		t.Fatalf("%s: %q is not an array (got %T)", what, key, v)
	}
	return rows
}

// e2eRoutes registers the real routes for a backend of this test's own. The
// registry is process-wide, so the id is unique and only this prefix is read.
func e2eRoutes(t *testing.T, cfg ConfigStore) (map[string]tool.HandlerFunc, string) {
	t.Helper()
	Register(Descriptor{
		ID:          e2eID,
		DisplayName: "e2e-mem",
		BinName:     "e2e-mem-no-such-binary",
		PrefPort:    41700,
		HealthPath:  "/healthz",
		GitHubURL:   "https://example.invalid/e2e-mem",
		Data:        e2eData{},
	})
	rr := &recordingRouter{routes: map[string]tool.HandlerFunc{}}
	prev := store
	t.Cleanup(func() { store = prev })
	RegisterRoutes(rr, cfg)
	return rr.routes, "/agentmemory/" + e2eID
}

// call drives one registered route and decodes its body.
func call(t *testing.T, routes map[string]tool.HandlerFunc, route string, v url.Values) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	h, ok := routes[route]
	if !ok {
		t.Fatalf("%s is not registered", route)
	}
	var w *httptest.ResponseRecorder
	var c *tool.Ctx
	if route[:4] == "GET " {
		w, c = get(v)
	} else {
		w, c = post(v)
	}
	h(c)
	if w.Body.Len() == 0 {
		return w, map[string]any{}
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: body is not a JSON object: %v (%s)", route, err, w.Body.String())
	}
	return w, body
}

// mustOK fails with the server's own message rather than a bare status.
func mustOK(t *testing.T, route string, w *httptest.ResponseRecorder, body map[string]any) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("%s: status %d, body %v", route, w.Code, body)
	}
	return body
}

// ── the field lists, transcribed from types.ts ───────────────────────

var (
	statusShape = shape{
		required: []string{"installed", "version", "running", "managed", "state", "pref_port", "bound_port", "base_url"},
		optional: []string{"started_at_ms"},
	}
	watchdogShape = shape{
		required: []string{"watching", "restarts", "hung_restarts", "consecutive_failures", "gave_up", "stopped_by_operator"},
		optional: []string{"last_reason", "last_restart_ms", "last_error", "next_attempt_ms", "gave_up_reason"},
	}
	resourcesShape = shape{
		required: []string{"rss_bytes", "rss_known", "data_dir_bytes", "data_dir_known"},
		optional: []string{"pid", "data_dir"},
	}
	storeShape = shape{
		required: []string{"version", "data_dir", "db_path", "bind", "capture_mode", "counts", "index", "storage", "ingest", "spool", "llm", "embedding"},
		optional: []string{"raw"},
	}
	overviewShape = shape{
		required: []string{"backend", "daemon", "settings", "resources", "autostart_lock", "watchdog"},
		optional: []string{"used_by", "store", "store_error", "store_reason"},
	}
	backendShape = shape{
		required: []string{"id", "name", "blurb", "has_data"},
		optional: []string{"icon", "github_url", "install_kind"},
	}
	projectRowShape = shape{
		required: []string{"workspace", "project", "page_count"},
		optional: []string{"last_updated", "briefing", "briefing_error"},
	}
	briefingShape = shape{
		required: []string{"counts", "activity_7d", "activity_30d", "pending_handoff_count", "pending_message_count", "cross_project_dependents", "cross_project_dependencies"},
		optional: []string{"last_observation_at", "recent_pages"},
	}
	handoffShape = shape{
		required: []string{"id"},
		optional: []string{"from_agent", "to_agent", "cwd", "created_at_ms"},
	}
	searchHitShape = shape{
		required: []string{"path"},
		optional: []string{"workspace", "project", "title", "kind", "snippet", "rank"},
	}
	backfillReportShape = shape{
		required: []string{"selected", "imported_sessions", "imported_events", "skipped_for_cap", "failed_sessions", "skipped_non_empty", "dry_run"},
		optional: []string{"workspace", "project"},
	}
	pageShape = shape{
		required: []string{"path", "body"},
		optional: []string{"workspace", "project", "title", "frontmatter"},
	}
	messageShape = shape{
		required: []string{"id"},
		optional: []string{"subject", "body", "from_agent", "from_workspace_id", "from_project_id", "state", "created_at", "claimed_at"},
	}
	pageWriteShape = shape{
		required: []string{"path"},
		optional: []string{"page_id", "output"},
	}
	pageDeleteShape = shape{
		required: []string{"path", "deleted"},
		optional: []string{"output"},
	}
	checkpointShape = shape{
		required: []string{"oid", "short_oid", "time", "summary"},
	}
	restoreResultShape = shape{
		required: []string{"path"},
		optional: []string{"page_id", "restored_from", "pre_checkpoint", "checkpoint"},
	}
	cancelResultShape = shape{
		required: []string{"handoff_id", "cancelled"},
		optional: []string{"state"},
	}
	// ExternalState. `has_token` rather than the token: the plaintext one
	// exists in exactly one response, the mint, and never appears here.
	externalShape = shape{
		required: []string{"enabled", "has_token", "url", "paths", "allowed_total", "rejected_total", "recent"},
	}
	externalRejectionShape = shape{
		required: []string{"time_ms", "method", "path", "client_ip", "reason", "status"},
	}
	// settingsFields is the FE's Settings type — its own six plus every
	// field of Tuning, which it intersects with.
	settingsFields = []string{
		"data_dir", "port", "enable_web", "autostart", "autostart_locked", "backfill_max_sessions",
		"base_path", "log_level",
		"allowed_hosts", "auth_token",
		"capture_mode", "project_strategy", "capture_assistant", "no_capture_prompts",
		"sanitize_extra_patterns", "sanitize_allowlist", "hook_rate_per_sec", "hook_rate_burst",
		"llm_provider", "llm_model", "embedding_provider", "embedding_model", "embedding_dim",
		"max_input_tokens", "max_output_tokens",
		"auto_improve_require_approval", "auto_improve_min_observations", "auto_improve_min_confidence",
		"auto_improve_max_proposals_per_run",
		"observation_retention_days", "observation_prune_batch", "hard_delete_after_days", "cold_threshold",
		"reranker", "decay_lambda", "decay_sigma", "decay_mu", "salience_default", "breadth_weight",
		"backfill_auto",
	}
)

// ── the sweep ────────────────────────────────────────────────────────

// TestDashboardPayloadsMatchTheFrontend drives every dashboard endpoint as an
// admin and checks the shape the panel reads.
func TestDashboardPayloadsMatchTheFrontend(t *testing.T) {
	routes, p := e2eRoutes(t, &fakeStore{
		enabled: true, admin: true,
		set: Settings{DataDir: "/srv/agent-memory/data", Port: 41700, EnableWeb: true, BackfillMaxSessions: 2000},
	})

	t.Run("backends", func(t *testing.T) {
		w, body := call(t, routes, "GET /agentmemory/backends", nil)
		mustOK(t, "backends", w, body)
		rows := list(t, "backends", body, "backends")
		found := false
		for _, r := range rows {
			row := r.(map[string]any)
			assertShape(t, "BackendInfo", row, backendShape)
			if row["id"] == e2eID {
				found = true
				if row["has_data"] != true {
					t.Errorf("has_data=%v — the switcher decides on it", row["has_data"])
				}
			}
		}
		if !found {
			t.Fatalf("the registered backend is missing from the switcher payload")
		}
	})

	t.Run("status is a complete Overview even with no daemon", func(t *testing.T) {
		w, body := call(t, routes, "GET "+p+"/status", nil)
		mustOK(t, "status", w, body)
		assertShape(t, "Overview", body, overviewShape)

		// The host this runs on has no ai-memory binary. That must be a
		// NAMED state, not an error: the panel draws a card either way.
		daemon := sub(t, "Overview", body, "daemon")
		assertShape(t, "Status", daemon, statusShape)
		if daemon["state"] != "not-installed" {
			t.Errorf("state %v, want not-installed on a host without the binary", daemon["state"])
		}
		if daemon["running"] != false || daemon["managed"] != false {
			t.Errorf("daemon claims to be up: %v", daemon)
		}

		// …and the store block must survive it. The two answer different
		// questions, and a store read that works while the daemon is down
		// is exactly the case that must not be blanked (PLAN §13.5).
		st := sub(t, "Overview", body, "store")
		assertShape(t, "StoreStatus", st, storeShape)
		if _, bad := body["store_error"]; bad {
			t.Errorf("a readable store must not carry an error: %v", body["store_error"])
		}
		counts := sub(t, "StoreStatus", st, "counts")
		for _, k := range []string{"pages_latest", "pages_all", "sessions", "observations"} {
			if _, ok := counts[k]; !ok {
				t.Errorf("counts is missing %q", k)
			}
		}
		assertShape(t, "IndexState", sub(t, "StoreStatus", st, "index"), shape{required: []string{
			"pages_rows", "pages_fts_rows", "observations_rows", "observations_fts_rows",
			"embedding_rows", "latest_pages_missing_embeddings",
		}})
		assertShape(t, "StorageState", sub(t, "StoreStatus", st, "storage"), shape{required: []string{
			"database_bytes", "reclaimable_bytes", "data_dir_free_bytes",
		}})
		assertShape(t, "IngestState", sub(t, "StoreStatus", st, "ingest"), shape{
			required: []string{"accepted", "dropped_by_policy", "shed_saturated", "shed_rate_limited"},
			optional: []string{"last_persisted_ms"},
		})
		assertShape(t, "SpoolState", sub(t, "StoreStatus", st, "spool"), shape{
			required: []string{"pending", "retries_total"},
			optional: []string{"oldest_age_ms"},
		})
		assertShape(t, "ProviderState", sub(t, "StoreStatus", st, "llm"), shape{
			required: []string{"status"},
			optional: []string{"provider", "model", "dim", "last_error_message"},
		})

		assertShape(t, "Resources", sub(t, "Overview", body, "resources"), resourcesShape)
		assertShape(t, "WatchdogState", sub(t, "Overview", body, "watchdog"), watchdogShape)
		assertShape(t, "AutostartLock", sub(t, "Overview", body, "autostart_lock"), shape{
			required: []string{"locked"},
			optional: []string{"used_by", "reason"},
		})
		assertShape(t, "BackendInfo", sub(t, "Overview", body, "backend"), backendShape)
		assertSettings(t, sub(t, "Overview", body, "settings"))
	})

	t.Run("settings round-trip", func(t *testing.T) {
		w, body := call(t, routes, "GET "+p+"/settings", nil)
		mustOK(t, "settings", w, body)
		assertShape(t, "SettingsResponse", body, shape{
			required: []string{"settings", "autostart_lock", "default_port"},
			optional: []string{"used_by"},
		})
		assertSettings(t, sub(t, "SettingsResponse", body, "settings"))

		// The secret never leaves in the clear — for anyone, admin included.
		w, body = call(t, routes, "POST "+p+"/settings", url.Values{
			"data_dir": {"/srv/agent-memory/data"}, "port": {"41700"},
			"enable_web": {"true"}, "autostart": {"false"}, "auth_token": {"hunter2"},
		})
		mustOK(t, "save settings", w, body)
		assertShape(t, "SaveSettingsResponse", body, shape{
			required: []string{"settings", "autostart_lock", "restart_pending"},
		})
		saved := sub(t, "SaveSettingsResponse", body, "settings")
		assertSettings(t, saved)
		if tok, _ := saved["auth_token"].(string); tok != secretMask {
			t.Errorf("auth_token came back as %q — it must be masked on every read", tok)
		}
	})

	t.Run("external access round-trip", func(t *testing.T) {
		// Read: off, no token, and a URL to call. The panel draws the whole
		// block from this one payload.
		w, body := call(t, routes, "GET "+p+"/external", nil)
		mustOK(t, "external", w, body)
		assertShape(t, "ExternalStateResponse", body, shape{required: []string{"external"}})
		ext := sub(t, "ExternalStateResponse", body, "external")
		assertShape(t, "ExternalState", ext, externalShape)
		if ext["enabled"] != false || ext["has_token"] != false {
			t.Errorf("a fresh backend must be closed: %v", ext)
		}
		// recent must be [] and not null — the FE maps over it.
		list(t, "ExternalState", ext, "recent")

		// Mint: the one response that carries a token, plus the state.
		w, body = call(t, routes, "POST "+p+"/external/token", nil)
		mustOK(t, "mint", w, body)
		assertShape(t, "ExternalTokenResponse", body, shape{required: []string{"token", "external"}})
		tok, _ := body["token"].(string)
		if !strings.HasPrefix(tok, externalTokenPrefix) {
			t.Errorf("minted token %q does not carry the prefix the FE tells the user to look for", tok)
		}
		assertShape(t, "ExternalState", sub(t, "ExternalTokenResponse", body, "external"), externalShape)

		// Reading it back says a token exists and never repeats it.
		w, body = call(t, routes, "GET "+p+"/external", nil)
		mustOK(t, "external after mint", w, body)
		if sub(t, "ExternalStateResponse", body, "external")["has_token"] != true {
			t.Error("has_token is false right after minting")
		}
		if strings.Contains(w.Body.String(), tok) {
			t.Fatal("the stored token was echoed back to the page")
		}

		// The switch and the revoke answer the same block.
		for _, route := range []string{"POST " + p + "/external", "POST " + p + "/external/revoke"} {
			args := url.Values(nil)
			if strings.HasSuffix(route, "/external") {
				args = url.Values{"enabled": {"true"}}
			}
			w, body = call(t, routes, route, args)
			mustOK(t, route, w, body)
			assertShape(t, "ExternalStateResponse", body, shape{required: []string{"external"}})
			assertShape(t, "ExternalState", sub(t, "ExternalStateResponse", body, "external"), externalShape)
		}
	})

	t.Run("a refused external request is reported field for field", func(t *testing.T) {
		be, ok := Get(e2eID)
		if !ok {
			t.Fatal("the e2e backend is not registered")
		}
		be.Mgr.ext = externalStats{}
		callExternal(ExternalProxy(e2eID), http.MethodGet,
			ExternalMountPath(e2eID)+"/api/v1/projects", "agmem_nope")

		w, body := call(t, routes, "GET "+p+"/external", nil)
		mustOK(t, "external", w, body)
		ext := sub(t, "ExternalStateResponse", body, "external")
		rows := list(t, "ExternalState", ext, "recent")
		if len(rows) != 1 {
			t.Fatalf("got %d recorded refusals, want 1", len(rows))
		}
		assertShape(t, "ExternalRejection", rows[0].(map[string]any), externalRejectionShape)
	})

	t.Run("projects carry their briefings", func(t *testing.T) {
		w, body := call(t, routes, "GET "+p+"/projects", nil)
		mustOK(t, "projects", w, body)
		rows := list(t, "ProjectsResponse", body, "projects")
		if len(rows) != 2 {
			t.Fatalf("got %d projects, want 2", len(rows))
		}
		row := rows[0].(map[string]any)
		assertShape(t, "ProjectRow", row, projectRowShape)
		br := sub(t, "ProjectRow", row, "briefing")
		assertShape(t, "ProjectBriefing", br, briefingShape)
		assertShape(t, "BriefingCounts", sub(t, "ProjectBriefing", br, "counts"), shape{required: []string{
			"pages_latest", "pages_all", "sessions", "observations", "evidence_rows",
		}})
		assertShape(t, "ActivityWindow", sub(t, "ProjectBriefing", br, "activity_7d"), shape{required: []string{
			"days", "sessions", "observations", "pages_updated",
		}})
		pages := list(t, "ProjectBriefing", br, "recent_pages")
		assertShape(t, "RecentPage", pages[0].(map[string]any), shape{
			required: []string{"path"},
			optional: []string{"title", "kind", "updated_at"},
		})
	})

	t.Run("health", func(t *testing.T) {
		w, body := call(t, routes, "GET "+p+"/health", nil)
		mustOK(t, "health", w, body)
		assertShape(t, "HealthReport", body, shape{
			required: []string{"doctor", "contamination", "collisions", "trial"},
			optional: []string{"error", "reason", "hint"},
		})
		// The per-project trial: a deliberate mode that stops capture for
		// every project that has not opted in, so the Health tab has to be
		// able to say it rather than leaving it to look like a broken hook.
		assertShape(t, "TrialCheck", sub(t, "HealthReport", body, "trial"), shape{
			required: []string{"active", "silenced"},
			optional: []string{"projects"},
		})
		doctor := sub(t, "HealthReport", body, "doctor")
		assertShape(t, "DoctorReport", doctor, shape{
			optional: []string{"workspace", "project", "since_days", "rows", "uncaptured", "error"},
		})
		drows := list(t, "DoctorReport", doctor, "rows")
		assertShape(t, "DoctorRow", drows[0].(map[string]any), shape{required: []string{
			"agent", "local_total", "local_recent", "captured", "uncaptured",
		}})
		cont := sub(t, "HealthReport", body, "contamination")
		assertShape(t, "ContaminationReport", cont, shape{
			required: []string{"sessions_misbucketed"},
			optional: []string{"findings", "error"},
		})
		// The findings the audit produced have to arrive as a list — the
		// table maps over them, and a null here empties a real finding.
		list(t, "ContaminationReport", cont, "findings")
		assertShape(t, "CollisionCheck", sub(t, "HealthReport", body, "collisions"), shape{
			required: []string{"checked", "scanned"},
			optional: []string{"reason", "collisions"},
		})
	})

	t.Run("handoffs and messages", func(t *testing.T) {
		w, body := call(t, routes, "GET "+p+"/handoffs", url.Values{"workspace": {"wick"}, "project": {"kasir-8c28230d"}})
		mustOK(t, "handoffs", w, body)
		rows := list(t, "HandoffsResponse", body, "handoffs")
		assertShape(t, "Handoff", rows[0].(map[string]any), handoffShape)

		w, body = call(t, routes, "GET "+p+"/messages", url.Values{"workspace": {"wick"}, "project": {"kasir-8c28230d"}})
		mustOK(t, "messages", w, body)
		assertShape(t, "MessagesResponse", body, shape{
			required: []string{"messages", "box"},
			optional: []string{"error", "reason", "hint"},
		})
		msgs := list(t, "MessagesResponse", body, "messages")
		assertShape(t, "Message", msgs[0].(map[string]any), messageShape)
		if body["box"] != "inbox" {
			t.Errorf("box=%v, want the read-only default", body["box"])
		}

		w, body = call(t, routes, "POST "+p+"/handoffs/cancel", url.Values{
			"workspace": {"wick"}, "project": {"kasir-8c28230d"}, "id": {"h-1"}, "confirm": {"true"},
		})
		mustOK(t, "cancel handoff", w, body)
		assertShape(t, "HandoffCancelResponse", body, shape{
			required: []string{"result", "warning"},
			optional: []string{"error", "reason", "hint"},
		})
		assertShape(t, "HandoffCancelResult", sub(t, "HandoffCancelResponse", body, "result"), cancelResultShape)
	})

	t.Run("search and page", func(t *testing.T) {
		w, body := call(t, routes, "GET "+p+"/search", url.Values{"q": {"kasir_prod_db"}})
		mustOK(t, "search", w, body)
		assertShape(t, "SearchResponse", body, shape{
			required: []string{"hits", "note"},
			optional: []string{"query", "error", "reason", "hint"},
		})
		hits := list(t, "SearchResponse", body, "hits")
		assertShape(t, "SearchHit", hits[0].(map[string]any), searchHitShape)

		// An empty query is answered with an empty LIST plus the note, not
		// with null: the Wiki tab renders the note either way.
		w, body = call(t, routes, "GET "+p+"/search", url.Values{"q": {"   "}})
		mustOK(t, "empty search", w, body)
		if rows := list(t, "SearchResponse", body, "hits"); len(rows) != 0 {
			t.Errorf("an empty query returned %d hits", len(rows))
		}

		w, body = call(t, routes, "GET "+p+"/page", url.Values{"path": {"pages/kasir_prod_db.md"}})
		mustOK(t, "page", w, body)
		assertShape(t, "PageResponse", body, shape{
			required: []string{"page"},
			optional: []string{"error", "reason", "hint"},
		})
		assertShape(t, "Page", sub(t, "PageResponse", body, "page"), pageShape)
	})

	t.Run("backfill, compact and sweep", func(t *testing.T) {
		v := url.Values{"workspace": {"wick"}, "project": {"kasir-8c28230d"}}
		for _, route := range []string{"POST " + p + "/backfill/preview", "POST " + p + "/backfill/run"} {
			w, body := call(t, routes, route, v)
			mustOK(t, route, w, body)
			assertShape(t, "BackfillResponse", body, shape{
				required: []string{"report", "request", "warning"},
				optional: []string{"error", "reason", "hint"},
			})
			assertShape(t, "BackfillReport", sub(t, "BackfillResponse", body, "report"), backfillReportShape)
			// The echo carries the RESOLVED cap — the only place the
			// number that truncated an import is visible.
			req := sub(t, "BackfillResponse", body, "request")
			assertShape(t, "BackfillRequestEcho", req, shape{
				required: []string{"scope", "dry_run", "force"},
				optional: []string{"session", "max_sessions"},
			})
			if req["max_sessions"] == nil {
				t.Error("request.max_sessions is empty — the resolved cap is what makes the echo worth sending")
			}
		}

		w, body := call(t, routes, "POST "+p+"/compact", url.Values{"confirm": {"true"}})
		mustOK(t, "compact", w, body)
		assertShape(t, "CompactResponse", body, shape{
			required: []string{"report", "warning"},
			optional: []string{"error", "reason", "hint"},
		})
		assertShape(t, "CompactReport", sub(t, "CompactResponse", body, "report"), shape{required: []string{"output"}})

		w, body = call(t, routes, "POST "+p+"/forget-sweep", url.Values{"dry_run": {"true"}})
		mustOK(t, "forget-sweep", w, body)
		assertShape(t, "SweepResponse", body, shape{
			required: []string{"report", "warning"},
			optional: []string{"error", "reason", "hint"},
		})
		assertShape(t, "SweepReport", sub(t, "SweepResponse", body, "report"), shape{required: []string{"dry_run", "output"}})
	})

	// Editing one project's memory (PLAN §22). Driven as an admin, because
	// all four are on the manage side — the viewer's 403 is asserted in
	// manage_handlers_test.go against the registered routes.
	t.Run("write, delete, checkpoints and restore", func(t *testing.T) {
		scoped := url.Values{"workspace": {"wick"}, "project": {"kasir-8c28230d"}}

		v := url.Values{"path": {"pages/kasir_prod_db.md"}, "body": {"# kasir_prod_db\n\nRead replica."}, "title": {"kasir_prod_db"}, "kind": {"fact"}}
		for k, vals := range scoped {
			v[k] = vals
		}
		w, body := call(t, routes, "POST "+p+"/page/write", v)
		mustOK(t, "write", w, body)
		assertShape(t, "PageWriteResponse", body, shape{
			required: []string{"result", "note"},
			optional: []string{"error", "reason", "hint"},
		})
		assertShape(t, "PageWriteResult", sub(t, "PageWriteResponse", body, "result"), pageWriteShape)
		// The reassurance is part of the payload, not a sentence the FE
		// invented: it is a claim about the BACKEND committing every write.
		if note, _ := body["note"].(string); !strings.Contains(note, "restored") {
			t.Errorf("note %q does not say the edit is recoverable", note)
		}

		d := url.Values{"path": {"pages/kasir_prod_db.md"}, "confirm": {"true"}}
		for k, vals := range scoped {
			d[k] = vals
		}
		w, body = call(t, routes, "POST "+p+"/page/delete", d)
		mustOK(t, "delete", w, body)
		assertShape(t, "PageDeleteResponse", body, shape{
			required: []string{"result", "warning"},
			optional: []string{"error", "reason", "hint"},
		})
		res := sub(t, "PageDeleteResponse", body, "result")
		assertShape(t, "PageDeleteResult", res, pageDeleteShape)
		if res["path"] != "pages/kasir_prod_db.md" {
			t.Errorf("a delete must name what it removed, got %v", res["path"])
		}

		w, body = call(t, routes, "GET "+p+"/checkpoints", url.Values{"limit": {"5"}})
		mustOK(t, "checkpoints", w, body)
		rows := list(t, "CheckpointsResponse", body, "checkpoints")
		assertShape(t, "Checkpoint", rows[0].(map[string]any), checkpointShape)

		r := url.Values{"path": {"pages/kasir_prod_db.md"}, "from": {"d544bc66fa5e"}}
		for k, vals := range scoped {
			r[k] = vals
		}
		w, body = call(t, routes, "POST "+p+"/page/restore", r)
		mustOK(t, "restore", w, body)
		assertShape(t, "RestoreResponse", body, shape{
			required: []string{"result", "note"},
			optional: []string{"error", "reason", "hint"},
		})
		assertShape(t, "RestoreResult", sub(t, "RestoreResponse", body, "result"), restoreResultShape)
	})

	t.Run("test connection reports the daemon that is not there", func(t *testing.T) {
		w, body := call(t, routes, "POST "+p+"/test", nil)
		mustOK(t, "test", w, body)
		// TestResult's optional half is only filled when the probe answers;
		// here it cannot, and that is the state the panel has to render.
		assertShape(t, "TestResult", body, shape{
			required: []string{"base_url", "health_path", "ok", "error"},
			optional: []string{"version", "counts", "data_dir", "store_error"},
		})
		if body["ok"] != false {
			t.Errorf("ok=%v with no daemon on the host", body["ok"])
		}
	})

	t.Run("logs", func(t *testing.T) {
		w, body := call(t, routes, "GET "+p+"/logs", nil)
		mustOK(t, "logs", w, body)
		if _, ok := body["logs"].(string); !ok {
			t.Errorf("logs is %T, want a string the panel can print", body["logs"])
		}
	})

	// The lifecycle endpoints are driven for their FAILURE shape: there is
	// no binary to spawn on this host, and the panel has to be able to say
	// so rather than show a spinner that never resolves.
	t.Run("start and restart fail with a message, not an empty body", func(t *testing.T) {
		for _, route := range []string{"POST " + p + "/start", "POST " + p + "/restart"} {
			w, body := call(t, routes, route, nil)
			if w.Code != http.StatusBadGateway {
				t.Fatalf("%s: status %d, want 502 when the binary is missing (body %v)", route, w.Code, body)
			}
			if msg, _ := body["error"].(string); msg == "" {
				t.Errorf("%s: no error message for the panel to show", route)
			}
		}
		// Install is not implemented yet and says so under its own status.
		w, body := call(t, routes, "POST "+p+"/install", nil)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("install: status %d, want 501", w.Code)
		}
		if msg, _ := body["error"].(string); msg == "" {
			t.Error("install: no error message")
		}
		// Stop answers a Status, which is what the panel re-renders from.
		w, body = call(t, routes, "POST "+p+"/stop", nil)
		mustOK(t, "stop", w, body)
		assertShape(t, "Status", body, statusShape)
	})

	// The host-wide roster behind the trial finding. Its two lists are what
	// the Health tab maps over, so they are checked for shape AND for never
	// being null — a null here is a TypeError, not an empty table.
	t.Run("project-policies", func(t *testing.T) {
		dir := t.TempDir()
		withProjects(t, "wick", ProjectFolder{ID: "8c28230d-aaaa", Name: "Kasir", Folder: dir})
		w, body := call(t, routes, "GET /agentmemory/project-policies", nil)
		mustOK(t, "project-policies", w, body)
		assertShape(t, "ProjectPolicyRoster", body, shape{
			required: []string{"trial", "recording", "silenced"},
		})
		assertShape(t, "TrialCheck", sub(t, "ProjectPolicyRoster", body, "trial"), shape{
			required: []string{"active", "silenced"},
			optional: []string{"projects"},
		})
		rows := list(t, "ProjectPolicyRoster", body, "recording")
		list(t, "ProjectPolicyRoster", body, "silenced")
		if len(rows) > 0 {
			assertShape(t, "ProjectPolicyRow", rows[0].(map[string]any), shape{
				required: []string{"id", "name", "value", "recording", "reason"},
			})
		}
	})

	t.Run("project-scope", func(t *testing.T) {
		dir := t.TempDir()
		withProjects(t, "wick", ProjectFolder{ID: "8c28230d-aaaa", Name: "Kasir", Folder: dir})
		w, body := call(t, routes, "GET /agentmemory/project-scope", url.Values{"project": {"8c28230d-aaaa"}})
		mustOK(t, "project-scope", w, body)
		assertShape(t, "ProjectScope", body, shape{required: []string{
			"project_id", "name", "folder", "workspace", "project", "source",
		}})
	})
}

// TestBlockedReadsCarryTheFixableReason drives the OTHER answer every panel
// read can give. A web API that is off and a daemon that is down are
// ordinary, fixable states, so they come back 200 with a named reason — the
// FE branches on that token, and a renamed one would turn "switch this on"
// into an empty table with no explanation (PLAN §13.1).
func TestBlockedReadsCarryTheFixableReason(t *testing.T) {
	const id = "e2e-blocked-mem"
	Register(Descriptor{
		ID: id, DisplayName: "e2e-blocked", BinName: "e2e-blocked-no-such-binary",
		PrefPort: 41800, HealthPath: "/healthz", Data: e2eFailingData{},
	})
	rr := &recordingRouter{routes: map[string]tool.HandlerFunc{}}
	prev := store
	t.Cleanup(func() { store = prev })
	RegisterRoutes(rr, &fakeStore{enabled: true, admin: true, set: Settings{EnableWeb: false}})
	p := "/agentmemory/" + id

	t.Run("a blocked list is a 200 with a reason, not a crash", func(t *testing.T) {
		w, body := call(t, rr.routes, "GET "+p+"/projects", nil)
		mustOK(t, "projects", w, body)
		assertShape(t, "ProjectsResponse (blocked)", body, shape{
			required: []string{"error", "reason", "hint"},
			optional: []string{"projects"},
		})
		if body["reason"] != reasonWebDisabled {
			t.Fatalf("reason %v, want %q — the FE switches on this token", body["reason"], reasonWebDisabled)
		}
		if hint, _ := body["hint"].(string); hint == "" {
			t.Error("a fixable state must carry the fix")
		}
	})

	t.Run("an unreadable store does not blank the daemon block", func(t *testing.T) {
		w, body := call(t, rr.routes, "GET "+p+"/status", nil)
		mustOK(t, "status", w, body)
		assertShape(t, "Overview (store unreadable)", body, overviewShape)
		if _, ok := body["store"]; ok {
			t.Error("a store that could not be read must be ABSENT, not an object of zeros")
		}
		if body["store_reason"] != reasonWebDisabled {
			t.Fatalf("store_reason %v, want %q", body["store_reason"], reasonWebDisabled)
		}
		if msg, _ := body["store_error"].(string); msg == "" {
			t.Error("store_error must say what happened")
		}
		// The half that still works keeps working.
		assertShape(t, "Status", sub(t, "Overview", body, "daemon"), statusShape)
		assertSettings(t, sub(t, "Overview", body, "settings"))
	})
}

// assertSettings checks a settings object against the FE's Settings type.
// Every field is required there — the form posts all of them back, so one
// missing from a response is a field the user cannot see and cannot keep.
func assertSettings(t *testing.T, got map[string]any) {
	t.Helper()
	assertShape(t, "Settings", got, shape{required: settingsFields})
}

// TestReadPayloadsAreTheSameForAViewer: the access split (PLAN §23) is about
// what someone may DO, not about a different panel. A viewer who silently got
// a thinner payload would see a half-empty dashboard and no reason for it.
func TestReadPayloadsAreTheSameForAViewer(t *testing.T) {
	admin := &fakeStore{enabled: true, admin: true, set: Settings{DataDir: "/srv/agent-memory/data", Port: 41700, EnableWeb: true}}
	routes, p := e2eRoutes(t, admin)

	reads := []struct {
		route string
		args  url.Values
	}{
		{"GET /agentmemory/backends", nil},
		{"GET " + p + "/status", nil},
		{"GET " + p + "/settings", nil},
		{"GET " + p + "/projects", nil},
		{"GET " + p + "/health", nil},
		{"GET " + p + "/handoffs", url.Values{"workspace": {"wick"}, "project": {"kasir-8c28230d"}}},
		{"GET " + p + "/messages", url.Values{"workspace": {"wick"}, "project": {"kasir-8c28230d"}}},
		{"GET " + p + "/search", url.Values{"q": {"kasir_prod_db"}}},
		{"GET " + p + "/page", url.Values{"path": {"pages/kasir_prod_db.md"}}},
	}

	for _, r := range reads {
		t.Run(r.route, func(t *testing.T) {
			store = admin
			wa, asAdmin := call(t, routes, r.route, r.args)
			mustOK(t, r.route+" (admin)", wa, asAdmin)

			store = &fakeStore{enabled: true, viewer: true, set: admin.set}
			wv, asViewer := call(t, routes, r.route, r.args)
			mustOK(t, r.route+" (viewer)", wv, asViewer)

			if a, v := keysOf(asAdmin), keysOf(asViewer); !equalStrings(a, v) {
				t.Fatalf("viewer got a different payload: admin %v, viewer %v", a, v)
			}
			// Deeper than the top level for the two that carry the data
			// the panel is actually made of.
			switch r.route {
			case "GET " + p + "/projects":
				ap := list(t, "admin", asAdmin, "projects")
				vp := list(t, "viewer", asViewer, "projects")
				if len(ap) != len(vp) {
					t.Fatalf("admin sees %d projects, viewer %d", len(ap), len(vp))
				}
				if !equalStrings(keysOf(ap[0].(map[string]any)), keysOf(vp[0].(map[string]any))) {
					t.Fatal("a project row differs between the roles")
				}
			case "GET " + p + "/status":
				if !equalStrings(keysOf(sub(t, "admin", asAdmin, "store")), keysOf(sub(t, "viewer", asViewer, "store"))) {
					t.Fatal("the store block differs between the roles")
				}
				// The masked secret is masked for BOTH — the split never
				// made an admin's settings read less careful.
				for role, body := range map[string]map[string]any{"admin": asAdmin, "viewer": asViewer} {
					s := sub(t, role, body, "settings")
					if tok, _ := s["auth_token"].(string); tok != "" && tok != secretMask {
						t.Fatalf("%s: auth_token leaked as %q", role, tok)
					}
				}
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
