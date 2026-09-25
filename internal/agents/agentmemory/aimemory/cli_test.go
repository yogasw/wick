package aimemory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// The fixtures below are verbatim captures from `ai-memory 2.4.0` on
// 2026-09-25, trimmed only where a repeated block adds nothing. No test in
// this file spawns the real binary: the runner is stubbed, which is also what
// keeps the suite runnable on a host that has never installed ai-memory.

const statusFixture = `{
  "version": "2.4.0",
  "data_dir": "/srv/mem/data",
  "bind": "127.0.0.1:49374",
  "db_path": "/srv/mem/data/db/memory.sqlite",
  "counts": {"pages_latest": 7, "pages_all": 16, "sessions": 7, "observations": 180},
  "derived": {
    "pages_rows": 16, "pages_fts_rows": 16,
    "observations_rows": 180, "observations_fts_rows": 180,
    "latest_pages_missing_embeddings": 0, "embedding_rows": 7
  },
  "storage": {"page_size": 4096, "database_bytes": 1069056, "reclaimable_bytes": 4096, "data_dir_free_bytes": 6809419776},
  "providers": {
    "llm": {"status": "disabled", "provider": null, "model": null, "dim": null, "last_error_message": null},
    "embedding": {"status": "ok", "provider": "local", "model": "all-MiniLM-L6-v2", "dim": 384, "last_error_message": null}
  },
  "spool": {"pending": 0, "oldest_age_ms": null, "retries_total": 0},
  "capture_mode": "denylist",
  "ingest": {"accepted": 0, "dropped_by_policy": 0, "shed_saturated": 0, "shed_rate_limited": 0, "last_persisted_ms": null},
  "client": {"server_url": "http://127.0.0.1:49374", "auth": false}
}`

// stubRun records the last invocation and replays a canned stdout. stdin is
// recorded too: a page body travels that way, and "was the body actually
// handed over" is the assertion a write test exists for.
type stubRun struct {
	out   string
	err   error
	dir   string
	env   []string
	args  []string
	stdin []byte
}

func (s *stubRun) run(_ context.Context, dir string, env, args []string, stdin []byte) ([]byte, error) {
	s.dir, s.env, s.args, s.stdin = dir, env, args, stdin
	return []byte(s.out), s.err
}

func newSource(s *stubRun) source { return source{run: s.run} }

// TestStoreStatusParse pins the translation from ai-memory's status document
// to the panel's neutral shape, including the two things a naive parse gets
// wrong: pages_all runs AHEAD of pages_latest (old versions), and null
// provider fields must land as empty strings, not as "<nil>".
func TestStoreStatusParse(t *testing.T) {
	st := &stubRun{out: statusFixture}
	got, err := newSource(st).StoreStatus(context.Background(), agentmemory.Conn{DataDir: "/srv/mem/data", BaseURL: "http://127.0.0.1:49374"})
	if err != nil {
		t.Fatalf("StoreStatus: %v", err)
	}
	if got.Version != "2.4.0" || got.DBPath != "/srv/mem/data/db/memory.sqlite" || got.CaptureMode != "denylist" {
		t.Fatalf("scalars: %+v", got)
	}
	if got.Counts.PagesLatest != 7 || got.Counts.PagesAll != 16 || got.Counts.Sessions != 7 || got.Counts.Observations != 180 {
		t.Fatalf("counts: %+v", got.Counts)
	}
	if got.Index.ObservationsFTSRows != 180 || got.Index.EmbeddingRows != 7 {
		t.Fatalf("index: %+v", got.Index)
	}
	if got.Storage.DatabaseBytes != 1069056 || got.Storage.ReclaimableBytes != 4096 {
		t.Fatalf("storage: %+v", got.Storage)
	}
	if got.LLM.Status != "disabled" || got.LLM.Provider != "" || got.LLM.Model != "" {
		t.Fatalf("a disabled llm must read as empty strings, got %+v", got.LLM)
	}
	if got.Embedding.Provider != "local" || got.Embedding.Dim != 384 {
		t.Fatalf("embedding: %+v", got.Embedding)
	}
	if got.Spool.OldestAgeMS != 0 || got.Ingest.LastPersistedMS != 0 {
		t.Fatalf("null timestamps must be 0, got spool=%+v ingest=%+v", got.Spool, got.Ingest)
	}
	if len(got.Raw) == 0 {
		t.Fatal("raw document must be carried through for fields wick has not modelled")
	}
	// status is store-wide: it takes no scope, and sending one would be a
	// silent lie about which numbers these are.
	if joined := strings.Join(st.args, " "); strings.Contains(joined, "--project") {
		t.Fatalf("status must not be scoped, got %v", st.args)
	}
	if !strings.Contains(strings.Join(st.args, " "), "--data-dir /srv/mem/data") {
		t.Fatalf("store must be passed through, got %v", st.args)
	}
}

// TestStatusTolerantOfLeadingNoise: a banner ahead of the JSON must not break
// the parse. The 2.4.0 binary logs to stderr, but a build that logs to stdout
// would otherwise blank the whole dashboard.
func TestStatusTolerantOfLeadingNoise(t *testing.T) {
	st := &stubRun{out: "INFO ai-memory starting\n" + statusFixture}
	got, err := newSource(st).StoreStatus(context.Background(), agentmemory.Conn{})
	if err != nil {
		t.Fatalf("StoreStatus: %v", err)
	}
	if got.Counts.Sessions != 7 {
		t.Fatalf("counts after noise: %+v", got.Counts)
	}
}

// TestArgsAssembly covers the flag layout every subcommand shares: subcommand
// first, then store, then scope, then the caller's own flags, --json last.
func TestArgsAssembly(t *testing.T) {
	got := args("backfill",
		agentmemory.Conn{DataDir: "/srv/mem"},
		agentmemory.ReadScope{Workspace: "qiscus", Project: "wick-8c28230d"},
		"--dry-run", "--max-sessions", "2000")
	want := []string{"backfill", "--data-dir", "/srv/mem", "--workspace", "qiscus", "--project", "wick-8c28230d", "--dry-run", "--max-sessions", "2000", "--json"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("args\n  got:  %v\n  want: %v", got, want)
	}
	// An unset store means "the backend's own default" — passing an empty
	// --data-dir would point it at the process's cwd instead.
	if bare := args("status", agentmemory.Conn{}, agentmemory.ReadScope{}); strings.Join(bare, " ") != "status --json" {
		t.Fatalf("bare args: %v", bare)
	}
}

// TestEnvCarriesTokenOutOfArgv: the server URL and the token go to the child's
// environment. argv is logged; a token in it would be logged with it.
func TestEnvCarriesTokenOutOfArgv(t *testing.T) {
	got := env(agentmemory.Conn{BaseURL: "http://127.0.0.1:49374", AuthToken: "s3cret"})
	if strings.Join(got, " ") != "AI_MEMORY_SERVER_URL=http://127.0.0.1:49374 AI_MEMORY_AUTH_TOKEN=s3cret" {
		t.Fatalf("env: %v", got)
	}
	if got := env(agentmemory.Conn{BaseURL: "http://x"}); len(got) != 1 {
		t.Fatalf("no token = no token var, got %v", got)
	}
}

// TestBackfillFlagsArePassedNotInvented is the guard on the one flag in this
// feature that multiplies stored data: --force appears when and only when the
// request asked for it (PLAN §11.1).
func TestBackfillFlagsArePassedNotInvented(t *testing.T) {
	const report = `{"workspace":"default","project":"proj2","selected":0,"imported_sessions":0,
	 "imported_events":0,"skipped_for_cap":0,"failed_sessions":0,"skipped_non_empty":true,"dry_run":true}`

	cases := []struct {
		name       string
		req        agentmemory.BackfillRequest
		wantFlags  []string
		unwantFlag string
	}{
		{
			name:       "plain dry run never forces",
			req:        agentmemory.BackfillRequest{DryRun: true, MaxSessions: 2000},
			wantFlags:  []string{"--dry-run", "--max-sessions", "2000"},
			unwantFlag: "--force",
		},
		{
			name:      "per-session import is force plus one session",
			req:       agentmemory.BackfillRequest{Force: true, Session: "abc-123"},
			wantFlags: []string{"--force", "--session", "abc-123"},
		},
		{
			name:       "no cap means no flag",
			req:        agentmemory.BackfillRequest{DryRun: true},
			wantFlags:  []string{"--dry-run"},
			unwantFlag: "--max-sessions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubRun{out: report}
			rep, err := newSource(st).Backfill(context.Background(), agentmemory.Conn{}, tc.req)
			if err != nil {
				t.Fatalf("Backfill: %v", err)
			}
			if !rep.SkippedNonEmpty || rep.Selected != 0 || !rep.DryRun {
				t.Fatalf("report: %+v", rep)
			}
			joined := strings.Join(st.args, " ")
			for _, f := range tc.wantFlags {
				if !strings.Contains(joined, f) {
					t.Fatalf("missing %q in %v", f, st.args)
				}
			}
			if tc.unwantFlag != "" && strings.Contains(joined, tc.unwantFlag) {
				t.Fatalf("unexpected %q in %v", tc.unwantFlag, st.args)
			}
		})
	}
}

// TestBackfillRunsInScopeDir: the scope's directory is the working directory
// of the command. ai-memory reads the project off the cwd, so running from the
// wrong place imports into the wrong project (PLAN §14).
func TestBackfillRunsInScopeDir(t *testing.T) {
	st := &stubRun{out: `{"selected":1,"dry_run":false}`}
	_, err := newSource(st).Backfill(context.Background(), agentmemory.Conn{},
		agentmemory.BackfillRequest{Scope: agentmemory.ReadScope{Dir: "/projects/x/files"}})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if st.dir != "/projects/x/files" {
		t.Fatalf("working dir: %q", st.dir)
	}
}

// TestHandoffsParse covers the null-heavy rows the real listing returns: a
// baton nobody has claimed has a null to_agent, and one raised outside a
// checkout has a null cwd.
func TestHandoffsParse(t *testing.T) {
	const fixture = `[
	  {"id":"01a0d412","from_agent":"claude-code","to_agent":null,"cwd":"/files/aim-case/proj2","created_at_ms":1790264356099},
	  {"id":"01a0d460","from_agent":"claude-code","to_agent":null,"cwd":null,"created_at_ms":1790269480441}
	]`
	st := &stubRun{out: fixture}
	got, err := newSource(st).Handoffs(context.Background(), agentmemory.Conn{}, agentmemory.ReadScope{Project: "proj2"}, 50)
	if err != nil {
		t.Fatalf("Handoffs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got[0].CWD != "/files/aim-case/proj2" || got[0].ToAgent != "" || got[0].CreatedAtMS != 1790264356099 {
		t.Fatalf("row 0: %+v", got[0])
	}
	if got[1].CWD != "" {
		t.Fatalf("a null cwd must read as empty, got %q", got[1].CWD)
	}
	if !strings.Contains(strings.Join(st.args, " "), "--limit 50") {
		t.Fatalf("limit not passed: %v", st.args)
	}
}

// TestHealthKeepsBothHalves: doctor and the contamination audit are resolved
// independently, so a failing doctor cannot hide a contamination finding —
// that finding is the one that means client data landed in another project.
func TestHealthKeepsBothHalves(t *testing.T) {
	const audit = `{"summary":{"sessions_misbucketed":3},"findings":[{"session_id":"abc"}]}`
	calls := 0
	src := source{run: func(_ context.Context, _ string, _, a []string, _ []byte) ([]byte, error) {
		calls++
		if a[0] == "doctor" {
			return nil, errors.New("server returned 404 Not Found: project not found")
		}
		return []byte(audit), nil
	}}
	rep, err := src.Health(context.Background(), agentmemory.Conn{}, agentmemory.ReadScope{Project: "gone"})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if calls != 2 {
		t.Fatalf("both diagnostics must run, got %d calls", calls)
	}
	if rep.Doctor.Error == "" || !strings.Contains(rep.Doctor.Error, "project not found") {
		t.Fatalf("doctor error must be carried, got %q", rep.Doctor.Error)
	}
	if rep.Contamination.SessionsMisbucketed != 3 || len(rep.Contamination.Findings) == 0 {
		t.Fatalf("contamination: %+v", rep.Contamination)
	}
}

// TestDoctorParse covers the empty-but-healthy document the CLI returns when a
// project has nothing to report.
func TestDoctorParse(t *testing.T) {
	const fixture = `{"workspace":"default","project":"proj2","server":"http://127.0.0.1:49374","since_days":30,"rows":[],"uncaptured":[]}`
	src := source{run: func(_ context.Context, _ string, _, a []string, _ []byte) ([]byte, error) {
		if a[0] == "doctor" {
			return []byte(fixture), nil
		}
		return []byte(`{"summary":{"sessions_misbucketed":0},"findings":[]}`), nil
	}}
	rep, err := src.Health(context.Background(), agentmemory.Conn{}, agentmemory.ReadScope{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if rep.Doctor.Error != "" || rep.Doctor.Project != "proj2" || rep.Doctor.SinceDays != 30 {
		t.Fatalf("doctor: %+v", rep.Doctor)
	}
	if string(rep.Doctor.Uncaptured) != "[]" {
		t.Fatalf("uncaptured must survive as raw JSON, got %q", rep.Doctor.Uncaptured)
	}
}

// TestCompactSkipsJSONFlag: `compact` is the one subcommand here with no
// --json mode (ai-memory 2.4.0 rejects it), so it must not go through args().
// It must carry --confirm, and its single line of prose must survive as the
// report rather than being parsed into numbers the CLI never promised.
func TestCompactSkipsJSONFlag(t *testing.T) {
	var got []string
	src := source{run: func(_ context.Context, dir string, _, a []string, _ []byte) ([]byte, error) {
		got = a
		if dir != "" {
			t.Fatalf("compact is store-wide: it must not run in a project dir, got %q", dir)
		}
		return []byte("Compacted: 1.0 MiB → 1020.0 KiB (24.0 KiB reclaimed).\n"), nil
	}}
	rep, err := src.Compact(context.Background(), agentmemory.Conn{DataDir: "/srv/mem"})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "--json") {
		t.Fatalf("compact must not be given --json, got %v", got)
	}
	if !strings.Contains(joined, "--confirm") || !strings.Contains(joined, "--data-dir /srv/mem") {
		t.Fatalf("compact args: %v", got)
	}
	if rep.Output != "Compacted: 1.0 MiB → 1020.0 KiB (24.0 KiB reclaimed)." {
		t.Fatalf("output must survive verbatim, got %q", rep.Output)
	}
}

// TestSourceIsACompactor pins the optional interface: the panel resolves it
// with a type assertion, which fails silently into a 501 if the method ever
// drifts off source.
func TestSourceIsACompactor(t *testing.T) {
	var _ agentmemory.Compactor = source{}
}
