package agentmemory

import "strings"

// Tuning is the daemon's configuration beyond the five knobs that start it:
// access, capture and privacy, model providers, retention, recall ranking and
// backfill (PLAN §13.4 blocks B-G).
//
// HOW IT REACHES THE DAEMON, and why it is not stored where the daemon reads.
// ai-memory's settings live in <data_dir>/config.toml — a file an operator may
// have hand-edited and commented. wick never rewrites it. Every key in it is
// ALSO settable through an `AI_MEMORY_*` environment variable, with a double
// underscore for a nested table (`AI_MEMORY_DECAY__OBSERVATION_RETENTION_DAYS`
// sets `[decay] observation_retention_days`) — verified against ai-memory
// 2.4.0 on 2026-09-25 by feeding each key a wrongly-typed value and watching
// the config loader name it in the error. So wick applies these on the launch
// line and leaves the file alone: nothing here can corrupt a config wick did
// not write, and clearing a field here restores the file's own value on the
// next start.
//
// Two consequences worth stating rather than discovering:
//
//   - A setting lands on the next START. A running daemon keeps what it was
//     started with, which is why the settings response carries
//     restart_pending.
//   - A field left at its zero value is NOT sent. That is what makes "unset"
//     mean "whatever config.toml says" instead of "zero".
//
// Every field name here is wick's own and backend-agnostic. The mapping to
// ai-memory's env names lives in the aimemory package, where it belongs.
type Tuning struct {
	// ── A. daemon ────────────────────────────────────────────────────

	// BasePath mounts the whole daemon under a prefix (/mcp, /api/v1, /hook
	// and the web UI all move below it), for running behind a reverse proxy.
	// Empty = mounted at the root.
	BasePath string `json:"base_path"`
	// LogLevel is the daemon's tracing filter ("error", "warn", "info",
	// "debug", "trace"). Empty = the backend's own default.
	LogLevel string `json:"log_level"`

	// ── B. access & security ─────────────────────────────────────────

	// AllowedHosts is the Host-header allowlist, comma-separated. Empty =
	// the backend's loopback default. This is a DNS-rebinding defence, not
	// the bind address: widening it is what makes a daemon reachable by
	// name from elsewhere on the network.
	AllowedHosts string `json:"allowed_hosts"`
	// AuthToken is the bearer token the daemon requires, stored the way
	// every other wick secret is (encrypted at rest) and resolved to
	// plaintext only when the launch line is built. Empty = no auth, which
	// is only safe while the bind stays on loopback.
	AuthToken string `json:"auth_token"`

	// ── C. capture & privacy ─────────────────────────────────────────

	// CaptureMode is the failure mode for a folder with no .ai-memory.toml
	// marker: "denylist" captures it anyway, "allowlist" emits nothing at
	// all. Empty = the backend's default (denylist).
	//
	// It is NOT a config-file key and cannot be set by environment: it
	// persists only through `install-hooks --apply`, which writes the
	// AGENT's own config files — something wick deliberately never does
	// (PLAN §3.3). wick instead bakes it onto the hook line it generates for
	// each spawn, so it applies to sessions wick starts and to nothing else.
	CaptureMode string `json:"capture_mode"`
	// ProjectStrategy is how a session's project is derived: "basename" of
	// the cwd, or "repo-root". Like CaptureMode it rides the generated hook
	// line. A .ai-memory.toml marker still wins over both, which is how wick
	// keeps two projects called "files" apart (PLAN §14.5).
	ProjectStrategy string `json:"project_strategy"`
	// CaptureAssistant stores the assistant's final turn on every Stop.
	// DOUBLE opt-in by design and left that way: this flag is the server
	// half, and the hook half is a separate flag on the generated hook line
	// — both are set from this one toggle, but the daemon refuses to store
	// the text unless its own half is on, so an external daemon does not
	// inherit the decision from wick's hook.
	//
	// Off by default because assistant text is the highest-risk thing in
	// this feature: it quotes code, credentials and file contents the store
	// would otherwise never see, and it is fed to the consolidation prompt —
	// so with a cloud LLM provider configured it leaves the host.
	CaptureAssistant bool `json:"capture_assistant"`
	// NoCapturePrompts drops the prompt-capture hook, so the user's own
	// words never reach the spool or the wire.
	//
	// Phrased as a negative on purpose: the zero value has to be "prompts
	// are captured", because that is what the rest of the feature assumes
	// and a silently prompt-less store looks like a broken hook.
	NoCapturePrompts bool `json:"no_capture_prompts"`
	// SanitizeExtraPatterns are additional redaction regexes, one per line.
	// The backend's built-in patterns always run; these are for identifiers
	// only this install knows are secret. An invalid regex aborts the
	// daemon's startup, so a bad one here shows up as a daemon that will
	// not start.
	SanitizeExtraPatterns string `json:"sanitize_extra_patterns"`
	// SanitizeAllowlist are substrings never to redact, one per line — for
	// a public identifier that collides with the generic *_KEY catch-all.
	SanitizeAllowlist string `json:"sanitize_allowlist"`
	// HookRatePerSec throttles hook ingest per actor. 0 = no limit.
	HookRatePerSec float64 `json:"hook_rate_per_sec"`
	// HookRateBurst is the bucket size. 0 = the rate itself.
	HookRateBurst float64 `json:"hook_rate_burst"`

	// ── D. model providers ───────────────────────────────────────────

	// LLMProvider turns consolidation on. Empty = zero-LLM: sessions are
	// still stored and searchable, but a stored fact is truncated at ~80
	// characters, which loses the tail of a long identifier (PLAN §9). A
	// cloud provider fixes that and sends session text off the host; a local
	// one fixes it and costs hundreds of MB of RAM (PLAN §12.6).
	LLMProvider string `json:"llm_provider"`
	LLMModel    string `json:"llm_model"`
	// EmbeddingProvider drives semantic search. Empty = the backend's
	// default, an in-process local model with no egress; "none" opts out
	// entirely and leaves full-text search only.
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingModel    string `json:"embedding_model"`
	EmbeddingDim      int    `json:"embedding_dim"`
	// MaxInputTokens / MaxOutputTokens bound the consolidation prompt. They
	// matter for a small local model, whose context cannot hold the default.
	MaxInputTokens  int `json:"max_input_tokens"`
	MaxOutputTokens int `json:"max_output_tokens"`
	// AutoImproveRequireApproval keeps the reviewer's proposals pending
	// until an admin approves them, instead of writing them straight into
	// the wiki.
	AutoImproveRequireApproval bool    `json:"auto_improve_require_approval"`
	AutoImproveMinObservations int     `json:"auto_improve_min_observations"`
	AutoImproveMinConfidence   float64 `json:"auto_improve_min_confidence"`
	AutoImproveMaxProposals    int     `json:"auto_improve_max_proposals_per_run"`

	// ── E. retention ─────────────────────────────────────────────────

	// ObservationRetentionDays prunes raw observations older than N days.
	//
	// 0 is the backend's default and means NEVER PRUNE. That is the right
	// default and the wrong thing to inherit silently: observations are the
	// store's growth driver, and pruning is irreversible — a pruned session
	// can never be re-consolidated, so its summary page becomes the only
	// surviving account of it. The UI makes this a decision, not a field.
	ObservationRetentionDays int `json:"observation_retention_days"`
	ObservationPruneBatch    int `json:"observation_prune_batch"`
	// HardDeleteAfterDays is when an evicted page stops being recoverable.
	HardDeleteAfterDays int `json:"hard_delete_after_days"`
	// ColdThreshold is the retention score below which a page is evicted.
	ColdThreshold float64 `json:"cold_threshold"`

	// ── F. recall ranking (advanced) ─────────────────────────────────

	// Reranker adds a final LLM relevance pass to queries. It sends the
	// query, page titles and snippets to the configured provider — so it is
	// an egress decision, not only a quality one.
	Reranker string `json:"reranker"`
	// The decay curve. Defaults put an unused episodic page below
	// ColdThreshold at roughly 80 days.
	DecayLambda     float64 `json:"decay_lambda"`
	DecaySigma      float64 `json:"decay_sigma"`
	DecayMu         float64 `json:"decay_mu"`
	SalienceDefault float64 `json:"salience_default"`
	BreadthWeight   float64 `json:"breadth_weight"`

	// ── G. backfill ──────────────────────────────────────────────────

	// BackfillAuto imports a project's existing local history the first time
	// wick sees it. Off by default: an import reads every local harness
	// transcript on the host, which is a lot of history to absorb without
	// being asked.
	BackfillAuto bool `json:"backfill_auto"`
}

// Loopback hosts — the set that keeps a daemon unreachable from the network.
var loopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "[::1]": true, "0:0:0:0:0:0:0:1": true}

// NonLoopbackHosts returns the entries of AllowedHosts that are NOT loopback.
//
// It exists so the warning in Settings can name what was added rather than
// saying "this may be unsafe": an allowlist widened to a LAN name is how a
// store of session transcripts becomes readable from another machine, and an
// operator who typed one hostname deserves to see that hostname back.
func (t Tuning) NonLoopbackHosts() []string {
	var out []string
	for _, h := range strings.Split(t.AllowedHosts, ",") {
		h = strings.TrimSpace(h)
		if h == "" || loopbackHosts[strings.ToLower(h)] {
			continue
		}
		out = append(out, h)
	}
	return out
}

// ExposedWithoutAuth reports the combination that has no safe reading: the
// daemon answers to a non-loopback host AND requires no token. Everything the
// store holds is then readable by anything that can route to the port.
func (t Tuning) ExposedWithoutAuth() bool {
	return len(t.NonLoopbackHosts()) > 0 && strings.TrimSpace(t.AuthToken) == ""
}

// Lines splits a newline-separated textarea field into its entries, dropping
// blanks. Used for the sanitize patterns, which are edited as text and sent to
// the backend as a list.
func Lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// CSV splits a comma-separated field the same way.
func CSV(s string) []string {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
