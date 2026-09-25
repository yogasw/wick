// Settings tab logic: the consequences the form has to state, and the ones it
// has to refuse to state until the user has confirmed them.
//
// Almost everything here is a SENTENCE rather than a value. That is the point
// of the tab: a store of client session transcripts has settings whose cost is
// invisible at the moment you flip them and obvious a month later, so the
// screen carries the cost next to the control (PLAN §13.5 point 2).

import type { ExternalRejection, ExternalState, Settings, Status } from "./types.js";

// emptySettings is the all-unset form: every field at the value that means
// "leave it to the backend". It is the shape the tab falls back to before a
// read lands, and the one place the field list is written out — a new knob
// added to the Go side shows up here as a type error rather than as a control
// that silently posts `undefined`.
export function emptySettings(): Settings {
  return {
    data_dir: "",
    port: 0,
    enable_web: true,
    autostart: false,
    autostart_locked: false,
    backfill_max_sessions: 0,
    base_path: "",
    log_level: "",
    allowed_hosts: "",
    auth_token: "",
    capture_mode: "",
    project_strategy: "",
    capture_assistant: false,
    no_capture_prompts: false,
    sanitize_extra_patterns: "",
    sanitize_allowlist: "",
    hook_rate_per_sec: 0,
    hook_rate_burst: 0,
    llm_provider: "",
    llm_model: "",
    embedding_provider: "",
    embedding_model: "",
    embedding_dim: 0,
    max_input_tokens: 0,
    max_output_tokens: 0,
    auto_improve_require_approval: false,
    auto_improve_min_observations: 0,
    auto_improve_min_confidence: 0,
    auto_improve_max_proposals_per_run: 0,
    observation_retention_days: 0,
    observation_prune_batch: 0,
    hard_delete_after_days: 0,
    cold_threshold: 0,
    reranker: "",
    decay_lambda: 0,
    decay_sigma: 0,
    decay_mu: 0,
    salience_default: 0,
    breadth_weight: 0,
    backfill_auto: false,
  };
}

// ── A. daemon ────────────────────────────────────────────────────────

// restartNote says whether a saved change is live yet. Settings land on the
// next start, so a running daemon keeps what it was started with — without
// this the page implies the daemon already moved to the new port or store.
export function restartNote(pending: boolean, running: boolean): string {
  if (!pending) return "Saved. The daemon will start with these settings.";
  if (!running) return "Saved. They take effect when the daemon starts.";
  return "Saved — but the running daemon still has the old settings. Restart it to apply them.";
}

// autostartNote explains a locked autostart control. A daemon something
// depends on cannot be left switched off, and a disabled control that says who
// holds it is honest where a switch that silently disagrees with the provider
// toggles is not (PLAN §10.3).
export function autostartNote(locked: boolean, reason: string | undefined): string {
  if (!locked) return "Start the daemon when wick boots.";
  return reason ?? "Autostart is forced on because a provider instance uses Agent Memory.";
}

// ── B. access & security ─────────────────────────────────────────────

// loopbackHosts is the set that keeps the daemon unreachable from the network.
const LOOPBACK = new Set(["localhost", "127.0.0.1", "::1", "[::1]"]);

// nonLoopbackHosts returns the entries that are NOT loopback, so the warning
// can name the host the operator typed rather than saying "this may be
// unsafe". Mirrors Tuning.NonLoopbackHosts on the Go side.
export function nonLoopbackHosts(allowed: string): string[] {
  return allowed
    .split(",")
    .map((h) => h.trim())
    .filter((h) => h !== "" && !LOOPBACK.has(h.toLowerCase()));
}

export type AccessWarning = { level: "warn" | "danger"; title: string; body: string };

// accessWarning is the security read on the current access settings.
//
// Two different states, and only one of them is an emergency:
//   - a non-loopback host WITH a token is a deliberate, defended choice;
//   - a non-loopback host WITHOUT one means every session transcript in the
//     store is readable by anything that can route to the port.
export function accessWarning(s: Settings): AccessWarning | null {
  const hosts = nonLoopbackHosts(s.allowed_hosts ?? "");
  if (hosts.length === 0) return null;
  const named = hosts.join(", ");
  if (!(s.auth_token ?? "").trim()) {
    return {
      level: "danger",
      title: "Reachable by name, with no token",
      body:
        `The daemon will answer requests for ${named} and requires no credential. Everything the store holds — every captured prompt, ` +
        "tool call and session summary, from every project on this host — is readable by anything that can reach the port. Set a bearer token, or take these hosts back out.",
    };
  }
  return {
    level: "warn",
    title: "Reachable beyond loopback",
    body: `${named} may reach this daemon. A bearer token is set, so a caller needs it — keep that token out of anything shared, and remove hosts you no longer use.`,
  };
}

// ── B2. reaching the store from outside wick ─────────────────────────

// EXTERNAL_WARNING is what the operator reads BEFORE the click, not after.
//
// The switch is one word long and the thing behind it is not: this store
// holds what agents saw inside client sessions — prompts, file contents,
// identifiers, whatever went past during support work — and turning this on
// makes all of it reachable from off this machine by anyone holding the
// token. Saying so next to the control is the whole mitigation.
export const EXTERNAL_WARNING =
  "This store holds what agents saw inside client sessions — prompts, tool calls and session summaries from every project on this host. " +
  "Turning this on lets anything that can reach wick read it with the access token. The daemon itself stays on loopback; wick checks the token and forwards.";

// externalStatus is the one-line reading of where the switch stands. The
// middle state is the one worth naming: a token exists but nothing is open
// yet, which is the normal halfway point of setting this up.
export function externalStatus(ext: ExternalState | null): string {
  if (!ext) return "Loading…";
  if (!ext.has_token) return "Closed. No access token exists, so every external request is refused.";
  if (!ext.enabled) return "Closed. A token exists but the switch is off — nothing outside wick can reach the store.";
  return "Open. A caller holding the access token can read this store from outside wick.";
}

// externalPathsNote says what is actually reachable, because "open" does not
// mean the whole daemon is. The backend's own web UI is deliberately not on
// the list and never should be.
export function externalPathsNote(ext: ExternalState | null): string {
  const paths = ext?.paths ?? [];
  if (paths.length === 0) return "";
  return `Only ${paths.join(" and ")} are forwarded. The backend's own web UI is not exposed.`;
}

// externalExample is the call to copy. Concrete beats a description: the
// commonest failure here is a script pointed at the daemon's own port, which
// is not reachable and never will be.
export function externalExample(ext: ExternalState | null): string {
  const url = ext?.url ?? "";
  if (!url) return "";
  return `curl -H "Authorization: Bearer <token>" ${url}/api/v1/projects`;
}

// REJECTION_REASONS turns the server's machine token into the sentence that
// answers "why can't my script reach it?".
const REJECTION_REASONS: Record<string, string> = {
  "external-access-off": "the switch was off",
  "no-token-created": "no access token existed",
  "no-bearer-sent": "no Authorization: Bearer header was sent",
  "wrong-token": "the token did not match",
  "path-not-exposed": "that path is not exposed externally",
};

// rejectionLine is one refused request as a person reads it.
export function rejectionLine(r: ExternalRejection): string {
  const why = REJECTION_REASONS[r.reason] ?? r.reason;
  return `${r.method} ${r.path} from ${r.client_ip} — refused (${r.status}) because ${why}.`;
}

// TOKEN_SHOWN_ONCE is the warning that rides the minted token. It is true —
// nothing stores the plaintext anywhere the page can read it again — so it
// has to be said at the moment the value is on screen.
export const TOKEN_SHOWN_ONCE =
  "Copy it now. This is the only time it is shown — wick stores it encrypted and never displays it again. A lost one is replaced by creating another.";

// ── C. capture & privacy ─────────────────────────────────────────────

// CAPTURE_MODES are the two failure modes for a folder with no marker.
export const CAPTURE_MODES = [
  {
    value: "denylist",
    label: "Denylist — capture unless excluded",
    body: "The backend's default. Any folder a wick session runs in is captured unless it carries a marker saying otherwise.",
  },
  {
    value: "allowlist",
    label: "Allowlist — capture only marked folders",
    body:
      "A folder with no .ai-memory.toml marker emits no lifecycle event at all. Recommended for a host that runs support sessions: a project is captured because someone said so, not because nobody said not to.",
  },
] as const;

// CAPTURE_MODE_SCOPE_NOTE is the limit of what this control does, and it is
// not a footnote. Upstream persists the capture mode through
// `install-hooks --apply`, which rewrites the AGENT's own settings files —
// something wick deliberately never does. wick bakes the mode onto the hook
// line it generates instead, so it governs sessions wick starts and nothing
// else on the host.
export const CAPTURE_MODE_SCOPE_NOTE =
  "Applies to sessions wick starts. wick writes this onto the hook command it generates per spawn rather than into your agent CLI's own config files, which it never edits — so an agent you run yourself from a terminal is unaffected.";

// CAPTURE_ASSISTANT_WARNING is the confirmation body for turning assistant
// capture on. It names the three things that actually change, because "stores
// assistant messages" sounds harmless and is not.
export const CAPTURE_ASSISTANT_WARNING =
  "The assistant's final message will be stored on every session stop. That text routinely quotes source code, file contents, credentials and customer data the store would otherwise never see — including from paths capture never touches. It is also fed to the consolidation prompt, so if you configure a cloud model provider it leaves this host.";

// PROMPT_CAPTURE_NOTE explains what switching prompt capture off really does,
// which is stronger than it sounds: the hook is not installed at all, so the
// text never enters the local spool or the wire.
export const PROMPT_CAPTURE_NOTE =
  "Off means the prompt hook is never installed, so what you type never reaches the spool or the network — not that the daemon receives it and discards it. Sessions are still captured; their pages just carry no prompt text.";

// PROJECT_STRATEGIES is how a session's project is derived from its folder.
export const PROJECT_STRATEGIES = [
  { value: "", label: "Backend default", body: "Leave the strategy to the backend." },
  {
    value: "basename",
    label: "Folder name",
    body: "The project is the name of the session's folder. wick's own marker overrides this, which is what keeps two projects both called \"files\" apart.",
  },
  {
    value: "repo-root",
    label: "Git repository root",
    body: "Every session resolves to the main repo root, collapsing subdirectories and worktrees into one project.",
  },
] as const;

// ── D. model providers ───────────────────────────────────────────────

// ProviderMode is one row of the trade-off table. It is shown as a table and
// not as a dropdown on purpose: no option here is free on all three axes, and
// picking one is a decision about client data, not a preference (PLAN §12.6).
export type ProviderMode = {
  id: "zero" | "local" | "cloud";
  label: string;
  facts: string;
  clientData: string;
  ram: string;
  danger: boolean;
};

export const PROVIDER_MODES: ProviderMode[] = [
  {
    id: "zero",
    label: "Zero-LLM (no provider)",
    facts: "Truncated at about 80 characters — the tail of a long identifier is lost from the stored fact.",
    clientData: "Never leaves this host.",
    ram: "Light (~130 MB).",
    danger: false,
  },
  {
    id: "local",
    label: "Local model (Ollama, LM Studio, vLLM)",
    facts: "Complete.",
    clientData: "Never leaves this host.",
    ram: "Heavy — hundreds of MB and up, on top of the daemon.",
    danger: false,
  },
  {
    id: "cloud",
    label: "Cloud provider",
    facts: "Complete.",
    clientData: "Session text is sent to the provider.",
    ram: "Light.",
    danger: true,
  },
];

// providerMode reads which row the current settings sit on. Empty provider is
// zero-LLM; a local runtime is recognised by name, because the difference that
// matters is egress and only the operator's own runtime keeps data here.
export function providerMode(s: Settings): ProviderMode["id"] {
  const p = (s.llm_provider ?? "").trim().toLowerCase();
  if (p === "") return "zero";
  if (["ollama", "lmstudio", "lm-studio", "vllm", "local", "openai-compat"].includes(p)) return "local";
  return "cloud";
}

// ZERO_LLM_NOTE is what running without a provider actually costs. It is the
// finding behind PLAN §9, stated where the decision is made.
export const ZERO_LLM_NOTE =
  "Without a model provider the store still captures sessions and stays searchable, but a synthesised fact is cut at roughly 80 characters — long identifiers lose their tail, so \"srv-melati-07\" can survive while a longer hostname does not.";

// RERANKER_NOTE marks the recall reranker as an egress decision, not only a
// quality one — it is easy to read as a local ranking tweak.
export const RERANKER_NOTE =
  "The LLM reranker sends the query, page titles and search snippets to your configured provider on every eligible search. With a cloud provider that is an egress path separate from consolidation.";

// ── E. retention ─────────────────────────────────────────────────────

// retentionSummary turns the retention number into what it means, because 0 —
// the default — is the one value that reads as "unset" and is not.
export function retentionSummary(days: number): { headline: string; body: string; danger: boolean } {
  if (!days || days <= 0) {
    return {
      headline: "Raw observations are never pruned",
      body:
        "This is the backend's default and the safest one, but it is a decision: raw observations are the store's growth driver, so the database grows for as long as agents run. Watch the storage figures on Analytics and set an age here before the disk decides for you.",
      danger: false,
    };
  }
  return {
    headline: `Raw observations older than ${days} days are deleted`,
    body:
      "Pruning is irreversible, and it is not the same as deleting a page: observations are the INPUT to consolidation, so a pruned session can never be re-consolidated — not by a better model, not by a fixed bug. Its summary page becomes the only surviving account of it. Only sessions already consolidated into a live page are touched.",
    danger: true,
  };
}

// SWEEP_WARNING is the ConfirmDialog body for a real sweep. Two irreversible
// things happen under one button and the second is the one that surprises
// people, so both are named.
export const SWEEP_WARNING =
  "A sweep evicts pages whose retention score has decayed below the cold threshold, and — if a retention age is set — permanently deletes raw observations older than it. Evicted pages remain recoverable until the hard-delete age; pruned observations do not, and their sessions can never be re-consolidated. Run the preview first.";

// ── G. backfill ──────────────────────────────────────────────────────

// backfillCapNote explains the cap in terms of what it silently drops. The
// backend's own default is 25, which quietly loses most of a long history and
// only shows up as skipped_for_cap in the report (PLAN §10.6).
export function backfillCapNote(max: number): string {
  if (!max || max <= 0) {
    return "Unset — wick's own ceiling of 2000 sessions is used. The backend's default is 25, which silently drops most of a real history.";
  }
  if (max <= 25) {
    return `At ${max}, a project with more sessions than that is imported only in part. The rest shows up as "skipped for cap" in the report, not as an error.`;
  }
  return `At most ${max} sessions per import. Anything beyond that is reported as "skipped for cap".`;
}

// ── shared ───────────────────────────────────────────────────────────

// isDirty reports whether the form differs from what is stored, so Save can
// say so. A deep compare over a flat record is enough — every field is a
// string, number or boolean.
export function isDirty(form: Settings | null, stored: Settings | null): boolean {
  if (!form || !stored) return false;
  return (Object.keys(form) as (keyof Settings)[]).some((k) => form[k] !== stored[k]);
}

// portNote names what port 0 means, which is not "no port".
export function portNote(port: number, defaultPort: number, st: Status | undefined): string {
  const pref = port && port > 0 ? port : defaultPort;
  const bound = st?.bound_port ?? 0;
  if (bound && bound !== pref) {
    return `Wanted ${pref}, but it was taken — the daemon is on ${bound}. It moves back when ${pref} is free.`;
  }
  return port && port > 0 ? `Binds 127.0.0.1:${port}.` : `Unset — the backend's own port (${defaultPort}) is used.`;
}
