package aimemory

// The panel's reader for ai-memory. Two surfaces, split the way the backend
// splits them (PLAN §13.2.1):
//
//   - the CLI, run per call with `--json`, for everything that has no HTTP
//     route: status, doctor, audit-contamination, handoffs, backfill;
//   - the REST API (/api/v1/…) for the project list and search, which only
//     exists when the daemon runs with --enable-web.
//
// MCP is used for exactly one call and no more: `memory_briefing`, the only
// surface that answers a project's own counters (Yoga, 2026-09-25 — see
// mcp.go). Every other MCP tool stays unused by the dashboard.
//
// Every shape parsed here was read off a real `ai-memory 2.4.0` run on
// 2026-09-25; the fixtures in cli_test.go are those captures verbatim.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/agentmemory"
	"github.com/yogasw/wick/pkg/safeexec"
)

// runner executes one CLI call and returns its stdout. dir is the working
// directory — it matters more than it looks: ai-memory resolves the scope of a
// project-scoped command from the process's cwd, so the same command run from
// the wrong place answers about a different project (PLAN §14).
//
// Swapped out in tests; nothing in the test suite ever spawns the real binary.
type runner func(ctx context.Context, dir string, env, args []string) ([]byte, error)

// source implements agentmemory.DataSource for ai-memory.
type source struct {
	run runner
}

// execCLI is the production runner. It takes stdout only: ai-memory writes its
// startup log line to stderr and the JSON document to stdout (verified), so
// mixing the two would put a log line in front of every parse.
func execCLI(ctx context.Context, dir string, env, args []string) ([]byte, error) {
	// Resolved by the core, not by PATH directly: wick's own installed copy
	// comes first, which is what stops the CLI-backed reads from reporting
	// "executable file not found in $PATH" while the daemon answers happily
	// over HTTP.
	bin, err := agentmemory.ResolveBackendBin(binName)
	if err != nil {
		return nil, fmt.Errorf("%s is not installed — install it from the Agent Memory panel: %w", binName, err)
	}
	cmd := safeexec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// The CLI reports a refusal as a one-line "Error: …" on stderr with a
		// non-zero exit. Carrying that line through is the difference between
		// "exit status 1" and "project 'x' not found in workspace 'default'".
		if msg := lastLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s %s: %s", binName, args[0], msg)
		}
		return nil, fmt.Errorf("%s %s: %w", binName, args[0], err)
	}
	return out, nil
}

// lastLine returns the final non-empty line of s, which is where the CLI puts
// its error after the startup log line.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// ── argument + env assembly ──────────────────────────────────────────

// args builds one CLI invocation: the subcommand, the store, the scope, then
// whatever the caller adds, then --json.
//
// --data-dir rides on the SUBCOMMAND here, not before it as in the daemon's
// launch line. Both positions work (verified on status, doctor, handoffs,
// search, audit-contamination and backfill), and the subcommand position is
// the one every subcommand's own --help documents.
func args(sub string, conn agentmemory.Conn, s agentmemory.ReadScope, extra ...string) []string {
	out := []string{sub}
	if d := strings.TrimSpace(conn.DataDir); d != "" {
		out = append(out, "--data-dir", d)
	}
	if w := strings.TrimSpace(s.Workspace); w != "" {
		out = append(out, "--workspace", w)
	}
	if p := strings.TrimSpace(s.Project); p != "" {
		out = append(out, "--project", p)
	}
	out = append(out, extra...)
	return append(out, "--json")
}

// env points the CLI at the daemon this panel manages. Several subcommands
// (handoffs, and anything that reaches /admin) are server calls, not local
// SQLite reads, so without this they would talk to whatever the default URL
// is. The token goes in env, never argv, because argv is logged.
func env(conn agentmemory.Conn) []string {
	e := []string{"AI_MEMORY_SERVER_URL=" + conn.BaseURL}
	if conn.AuthToken != "" {
		e = append(e, "AI_MEMORY_AUTH_TOKEN="+conn.AuthToken)
	}
	return e
}

// decode unmarshals a CLI document, trimming anything before the JSON in case
// a future build writes a banner to stdout.
func decode(out []byte, v any) error {
	b := bytes.TrimSpace(out)
	if i := bytes.IndexAny(b, "{["); i > 0 {
		b = b[i:]
	}
	if len(b) == 0 {
		return fmt.Errorf("%s: empty output", binName)
	}
	return json.Unmarshal(b, v)
}

// ── DataSource ───────────────────────────────────────────────────────

// StoreStatus runs `status --json`. Store-wide by definition: the subcommand
// takes no --project, which is exactly why its numbers must never be shown in
// the same card as a project's (PLAN §13.5).
func (s source) StoreStatus(ctx context.Context, conn agentmemory.Conn) (*agentmemory.StoreStatus, error) {
	out, err := s.run(ctx, "", env(conn), args("status", conn, agentmemory.ReadScope{}))
	if err != nil {
		return nil, err
	}
	var raw statusJSON
	if err := decode(out, &raw); err != nil {
		return nil, fmt.Errorf("parse %s status: %w", binName, err)
	}
	return raw.toStoreStatus(bytes.TrimSpace(out)), nil
}

// Health runs the two read-only diagnostics. They are reported side by side
// but resolved independently: a doctor that cannot run must not hide a
// contamination finding, which is the one that means client data landed in the
// wrong project.
func (s source) Health(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope) (*agentmemory.HealthReport, error) {
	rep := &agentmemory.HealthReport{}

	if out, err := s.run(ctx, sc.Dir, env(conn), args("doctor", conn, sc)); err != nil {
		rep.Doctor.Error = err.Error()
	} else if err := decode(out, &rep.Doctor); err != nil {
		rep.Doctor.Error = fmt.Sprintf("parse doctor: %v", err)
	}

	if out, err := s.run(ctx, sc.Dir, env(conn), args("audit-contamination", conn, sc)); err != nil {
		rep.Contamination.Error = err.Error()
	} else {
		var raw contaminationJSON
		if err := decode(out, &raw); err != nil {
			rep.Contamination.Error = fmt.Sprintf("parse audit-contamination: %v", err)
		} else {
			rep.Contamination.SessionsMisbucketed = raw.Summary.SessionsMisbucketed
			rep.Contamination.Findings = raw.Findings
		}
	}
	return rep, nil
}

// Handoffs runs `handoffs --json`. This one is a server call — it 404s with
// "project not found" when the scope names a project the store has never seen,
// which the handler surfaces as-is rather than as an empty list.
func (s source) Handoffs(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, limit int) ([]agentmemory.Handoff, error) {
	extra := []string{}
	if limit > 0 {
		extra = append(extra, "--limit", strconv.Itoa(limit))
	}
	out, err := s.run(ctx, sc.Dir, env(conn), args("handoffs", conn, sc, extra...))
	if err != nil {
		return nil, err
	}
	var rows []handoffJSON
	if err := decode(out, &rows); err != nil {
		return nil, fmt.Errorf("parse %s handoffs: %w", binName, err)
	}
	outRows := make([]agentmemory.Handoff, 0, len(rows))
	for _, r := range rows {
		outRows = append(outRows, agentmemory.Handoff{
			ID:          r.ID,
			FromAgent:   str(r.FromAgent),
			ToAgent:     str(r.ToAgent),
			CWD:         str(r.CWD),
			CreatedAtMS: r.CreatedAtMS,
		})
	}
	return outRows, nil
}

// Backfill runs one import. It passes --force through exactly as asked and
// never adds it: a forced run re-imports sessions the store already has, and
// observations do not dedupe (PLAN §11.1). The core gates that behind an
// explicit confirmation; this function's job is to not quietly add flags.
func (s source) Backfill(ctx context.Context, conn agentmemory.Conn, req agentmemory.BackfillRequest) (*agentmemory.BackfillReport, error) {
	extra := []string{}
	if req.DryRun {
		extra = append(extra, "--dry-run")
	}
	if req.Force {
		extra = append(extra, "--force")
	}
	if sid := strings.TrimSpace(req.Session); sid != "" {
		extra = append(extra, "--session", sid)
	}
	if req.MaxSessions > 0 {
		extra = append(extra, "--max-sessions", strconv.Itoa(req.MaxSessions))
	}
	out, err := s.run(ctx, req.Scope.Dir, env(conn), args("backfill", conn, req.Scope, extra...))
	if err != nil {
		return nil, err
	}
	var rep agentmemory.BackfillReport
	if err := decode(out, &rep); err != nil {
		return nil, fmt.Errorf("parse %s backfill: %w", binName, err)
	}
	return &rep, nil
}

// Compact reclaims free database pages.
//
// It is the one subcommand the panel drives that has NO --json mode, so it
// does not go through args(): that helper always appends --json, which this
// command rejects. Its output is one line of prose, carried through as-is.
//
// --confirm is required by the binary itself. It is passed here because the
// core only ever calls this after its own confirmation gate (handlers.go) —
// nothing in this file decides that compaction is wanted.
func (s source) Compact(ctx context.Context, conn agentmemory.Conn) (*agentmemory.CompactReport, error) {
	cmd := []string{"compact", "--confirm"}
	if d := strings.TrimSpace(conn.DataDir); d != "" {
		cmd = append(cmd, "--data-dir", d)
	}
	out, err := s.run(ctx, "", env(conn), cmd)
	if err != nil {
		return nil, err
	}
	return &agentmemory.CompactReport{Output: strings.TrimSpace(string(out))}, nil
}

// ── CLI JSON shapes ──────────────────────────────────────────────────

// statusJSON mirrors `status --json` as ai-memory 2.4.0 emits it. Nullable
// fields are pointers because the backend writes JSON null for "never
// happened", and 0 would read as a real measurement.
type statusJSON struct {
	Version string `json:"version"`
	DataDir string `json:"data_dir"`
	DBPath  string `json:"db_path"`
	Bind    string `json:"bind"`
	Counts  struct {
		PagesLatest  int64 `json:"pages_latest"`
		PagesAll     int64 `json:"pages_all"`
		Sessions     int64 `json:"sessions"`
		Observations int64 `json:"observations"`
	} `json:"counts"`
	Derived struct {
		PagesRows              int64 `json:"pages_rows"`
		PagesFTSRows           int64 `json:"pages_fts_rows"`
		ObservationsRows       int64 `json:"observations_rows"`
		ObservationsFTSRows    int64 `json:"observations_fts_rows"`
		EmbeddingRows          int64 `json:"embedding_rows"`
		LatestPagesMissingEmbs int64 `json:"latest_pages_missing_embeddings"`
	} `json:"derived"`
	Storage struct {
		DatabaseBytes    int64 `json:"database_bytes"`
		ReclaimableBytes int64 `json:"reclaimable_bytes"`
		FreeBytes        int64 `json:"data_dir_free_bytes"`
	} `json:"storage"`
	Providers struct {
		LLM       providerJSON `json:"llm"`
		Embedding providerJSON `json:"embedding"`
	} `json:"providers"`
	Spool struct {
		Pending      int64  `json:"pending"`
		OldestAgeMS  *int64 `json:"oldest_age_ms"`
		RetriesTotal int64  `json:"retries_total"`
	} `json:"spool"`
	CaptureMode string `json:"capture_mode"`
	Ingest      struct {
		Accepted        int64  `json:"accepted"`
		DroppedByPolicy int64  `json:"dropped_by_policy"`
		ShedSaturated   int64  `json:"shed_saturated"`
		ShedRateLimited int64  `json:"shed_rate_limited"`
		LastPersistedMS *int64 `json:"last_persisted_ms"`
	} `json:"ingest"`
}

type providerJSON struct {
	Status   string  `json:"status"`
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
	Dim      *int    `json:"dim"`
	LastErr  *string `json:"last_error_message"`
}

func (p providerJSON) toState() agentmemory.ProviderState {
	st := agentmemory.ProviderState{
		Status:   p.Status,
		Provider: str(p.Provider),
		Model:    str(p.Model),
		LastErr:  str(p.LastErr),
	}
	if p.Dim != nil {
		st.Dim = *p.Dim
	}
	return st
}

// toStoreStatus translates one status document into the panel's neutral shape,
// keeping the original alongside so a field wick has not modelled is still
// reachable from the FE.
func (r statusJSON) toStoreStatus(raw []byte) *agentmemory.StoreStatus {
	return &agentmemory.StoreStatus{
		Version:     r.Version,
		DataDir:     r.DataDir,
		DBPath:      r.DBPath,
		Bind:        r.Bind,
		CaptureMode: r.CaptureMode,
		Counts: agentmemory.StoreCounts{
			PagesLatest:  r.Counts.PagesLatest,
			PagesAll:     r.Counts.PagesAll,
			Sessions:     r.Counts.Sessions,
			Observations: r.Counts.Observations,
		},
		Index: agentmemory.IndexState{
			PagesRows:               r.Derived.PagesRows,
			PagesFTSRows:            r.Derived.PagesFTSRows,
			ObservationsRows:        r.Derived.ObservationsRows,
			ObservationsFTSRows:     r.Derived.ObservationsFTSRows,
			EmbeddingRows:           r.Derived.EmbeddingRows,
			LatestPagesMissingEmbed: r.Derived.LatestPagesMissingEmbs,
		},
		Storage: agentmemory.StorageState{
			DatabaseBytes:    r.Storage.DatabaseBytes,
			ReclaimableBytes: r.Storage.ReclaimableBytes,
			FreeBytes:        r.Storage.FreeBytes,
		},
		Ingest: agentmemory.IngestState{
			Accepted:        r.Ingest.Accepted,
			DroppedByPolicy: r.Ingest.DroppedByPolicy,
			ShedSaturated:   r.Ingest.ShedSaturated,
			ShedRateLimited: r.Ingest.ShedRateLimited,
			LastPersistedMS: num(r.Ingest.LastPersistedMS),
		},
		Spool: agentmemory.SpoolState{
			Pending:      r.Spool.Pending,
			OldestAgeMS:  num(r.Spool.OldestAgeMS),
			RetriesTotal: r.Spool.RetriesTotal,
		},
		LLM:       r.Providers.LLM.toState(),
		Embedding: r.Providers.Embedding.toState(),
		Raw:       json.RawMessage(raw),
	}
}

// contaminationJSON mirrors `audit-contamination --json`. Findings stay raw:
// the empty run this was captured from shows the wrapper, not a finding's
// fields, and inventing that struct would be inventing the very rows an
// operator has to act on.
type contaminationJSON struct {
	Summary struct {
		SessionsMisbucketed int64 `json:"sessions_misbucketed"`
	} `json:"summary"`
	Findings json.RawMessage `json:"findings"`
}

// handoffJSON mirrors one row of `handoffs --json`.
type handoffJSON struct {
	ID          string  `json:"id"`
	FromAgent   *string `json:"from_agent"`
	ToAgent     *string `json:"to_agent"`
	CWD         *string `json:"cwd"`
	CreatedAtMS int64   `json:"created_at_ms"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func num(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
