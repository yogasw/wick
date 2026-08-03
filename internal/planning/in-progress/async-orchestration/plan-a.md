# A — Mention Router: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `@handle task` in a human or agent turn becomes a message to a live agent or a spawn of a role, with a feedback line telling the author what was dispatched.

**Architecture:** A `Router` in the delegation package resolves each `ParseMentions` hit against live handles first, then visible role keys, and dispatches through the existing `SendMessage` / `Run` services. A `TurnObserver` accumulates agent turn text from the pool's event callbacks and routes on `Done`; the human path routes inside `sendAgent`. A roster block rides in the task envelope at spawn.

**Tech Stack:** Go 1.2x, GORM, Svelte 5 (no FE work in this plan beyond none — the panel already renders delegations and messages).

**Spec:** [a-mention-router.md](a-mention-router.md) — depends on Q (plan-q.md) being done.

## Global Constraints

- **Never `git commit`.** The user commits. Every task ends at "tests pass".
- UI copy is English. Samples use `abc.com` / `example.com`.
- Zerolog: `l := log.With().Str("component", "x").Logger()`.
- Postgres and sqlite dialects only. Tests with `-count=1`.
- No dead knobs.

---

## File Structure

**Created:**

| Path | Responsibility |
|---|---|
| `internal/agents/delegation/roster.go` | `ResolveTargets` — live handles ∪ visible roles, one lookup struct. |
| `internal/agents/delegation/roster_test.go` | Precedence + scoping tests. |
| `internal/agents/delegation/router.go` | `Router.Route` — parse, resolve, dispatch, feedback line. |
| `internal/agents/delegation/router_test.go` | Dispatch matrix, multi-mention async forcing, feedback text. |
| `internal/agents/delegation/observer.go` | `TurnObserver` — per-(session,agent) text accumulator, routes on Done. |
| `internal/agents/delegation/observer_test.go` | Accumulate/flush/reset semantics. |

**Modified:**

| Path | Change |
|---|---|
| `internal/agents/config/general.go` | `+ SubAgentsMentionRouter bool` (default true). |
| `internal/pkg/api/delegation_limits.go` | Read the new key into the router's enabled check. |
| `internal/pkg/api/server.go` | Construct observer; hook `OnEvent`/`OnExit`; construct Router. |
| `internal/tools/agents/handler.go` | `sendAgent` routes the human's text. |
| `internal/agents/delegation/run.go` | Roster block appended by `composeTask`. |
| `internal/agents/delegation/format.go` | Shared roster renderer used by both spawn and inbox paths. |
| `internal/agents/system-prompt/immutable.md` | Mention syntax section. |
| `docs/guide/agents/sub-agents.md` | Mention docs. |

---

### Task 1: Target resolution

**Files:**
- Create: `internal/agents/delegation/roster.go`, `roster_test.go`

**Interfaces:**
- Consumes: `Repo.ListByRoot`, `Repo.ListProfilesInScopes` (both exist).
- Produces:

```go
type TargetKind int
const (
	TargetUnknown TargetKind = iota
	TargetAgent              // a live handle in this tree
	TargetRole               // a spawnable profile key
)

type Target struct {
	Kind   TargetKind
	Handle string // set for TargetAgent
	Role   string // set for TargetRole
}

// Resolver is a snapshot of what @tokens can mean for one tree+project.
type Resolver struct { /* handles map[string]bool; roles map[string]bool */ }

func (s *Service) NewResolver(ctx context.Context, rootID, projectID string) (*Resolver, error)
func (r *Resolver) Resolve(token string) Target
func (r *Resolver) AllNames() []string // roster arg for ParseMentions
```

- [ ] **Step 1: Failing test**

```go
func TestResolvePrefersLiveAgentOverRole(t *testing.T) {
	r := &Resolver{
		handles: map[string]bool{"reviewer": true, "reviewer-2": true},
		roles:   map[string]bool{"reviewer": true, "researcher": true},
	}
	if got := r.Resolve("reviewer"); got.Kind != TargetAgent || got.Handle != "reviewer" {
		t.Fatalf("reviewer → %+v, want live agent", got)
	}
	if got := r.Resolve("researcher"); got.Kind != TargetRole || got.Role != "researcher" {
		t.Fatalf("researcher → %+v, want role", got)
	}
	if got := r.Resolve("nobody"); got.Kind != TargetUnknown {
		t.Fatalf("nobody → %+v, want unknown", got)
	}
}

func TestNewResolverExcludesTerminalHandlesAndDisabledRoles(t *testing.T) {
	// seed: root with one running delegation (handle "worker"), one done
	// ("worker-2"); profiles: enabled "researcher", disabled "ghost".
	// Assert: worker resolves TargetAgent; worker-2 does NOT (its agent is
	// gone — messaging it would queue mail nobody drains); researcher is a
	// role; ghost is unknown.
}
```

- [ ] **Step 2: Run — FAIL** (`undefined: Resolver`).
- [ ] **Step 3: Implement.** `NewResolver`: handles from
      `ListByRoot(rootID)` filtered to non-terminal statuses, plus
      `entity.LeaderHandle` ("main") always; roles from
      `ListProfilesInScopes(projectID, false)` keyed by `profile.Key`, skipping
      `Disabled`. `Resolve` checks handles first — precedence is the whole contract.
- [ ] **Step 4: `go test ./internal/agents/delegation/ -run TestResolve -count=1` — PASS.**

---

### Task 2: Config key `sub_agents_mention_router`

**Files:**
- Modify: `internal/agents/config/general.go`, `internal/pkg/api/delegation_limits.go`
- Test: extend the existing `delegation_limits` test file in `internal/pkg/api/`

**Interfaces:**
- Produces: `GeneralConfig.SubAgentsMentionRouter bool` (default `true`);
  `delegation.Limits.MentionRouter bool` populated by the provider.

- [ ] **Step 1: Failing test** — provider test: absent key → true; `"false"` row → false.

```go
func TestMentionRouterKeyIsLiveRead(t *testing.T) {
	// pattern-match the existing tests around delegationLimitsProvider:
	// seed configs table row agents/sub_agents_mention_router = "false",
	// assert current().MentionRouter == false; delete row, assert true.
}
```

- [ ] **Step 2: FAIL** (`unknown field MentionRouter`).
- [ ] **Step 3: Implement.**

`general.go` (Sub-agents group, after `SubAgentsInboxCap`):

```go
	SubAgentsMentionRouter bool `wick:"bool;group=Sub-agents;desc=Let @role or @handle at the start of a line in a message dispatch work to that agent. Off = mentions stay plain text. Default: on."`
```

Default `true` in `DefaultGeneralConfig`. `delegation_limits.go`:

```go
	lim.MentionRouter = def.SubAgentsMentionRouter
	if v := p.cfg.GetOwned("agents", "sub_agents_mention_router"); v != "" {
		lim.MentionRouter = v == "true"
	}
```

`governor.go` `Limits` gains `MentionRouter bool` (no normalize clause — false is a valid
operator choice, not a zero-value accident; document that in a comment).

- [ ] **Step 4: PASS**, including a `go build ./...`.

---

### Task 3: The router

**Files:**
- Create: `internal/agents/delegation/router.go`, `router_test.go`

**Interfaces:**
- Consumes: `ParseMentions` (mention.go), `Resolver` (Task 1), `Service.SendMessage`
  (mailbox.go:82, `SendInput{RootID, FromHandle, ToHandle, Body, Kind}` — read the struct
  and match it exactly), `Service.Run` (run.go:255), `Limits.MentionRouter`.
- Produces:

```go
// Dispatch is one mention acted on.
type Dispatch struct {
	Token        string // the @name as written
	Kind         TargetKind
	DelegationID string // spawned role
	Queued       bool
	QueuePos     int
	Err          string // governor refusal message, when refused
}

type RouteInput struct {
	SessionID  string // the session whose text this is (leader or child)
	ProjectID  string
	FromHandle string // "main" for the leader/human path
	Author     string // "human" | "agent" — humans do not consume hops
	Text       string
	TriggeredBy string // human user id at the root
}

func (s *Service) Route(ctx context.Context, in RouteInput) []Dispatch
func FormatDispatches(ds []Dispatch) string // "" when empty
```

- [ ] **Step 1: Failing tests**

```go
func TestRouteSpawnsARoleAndMessagesALiveAgent(t *testing.T) {
	// svc with fake runner; profile "researcher"; a live delegation with
	// handle "worker". Text:
	//   "@researcher find the changelog\n@worker status?"
	// Assert: one Run happened (researcher), one SendMessage (worker,
	// Kind tell), two Dispatches in input order.
}

func TestMultiMentionForcesAsyncSessionSink(t *testing.T) {
	// two role mentions; profile default mode sync. Assert both Requests
	// passed to Run carry Mode=async, DeliverySink=session.
}

func TestSingleMentionUsesRoleDefaultMode(t *testing.T) { /* sync stays sync */ }

func TestRouterDisabledDispatchesNothing(t *testing.T) {
	// Limits.MentionRouter=false → zero dispatches, text untouched.
}

func TestRouteFalsePositiveCorpusDispatchesNothing(t *testing.T) {
	// reuse the corpus strings from mention_test.go verbatim:
	// "@media check", "email a@b.com", fenced "```\n@worker hi\n```" etc.
}

func TestFormatDispatches(t *testing.T) {
	got := FormatDispatches([]Dispatch{
		{Token: "log-investigator", Kind: TargetRole, DelegationID: "D-7f3a"},
		{Token: "docs-investigator", Kind: TargetRole, DelegationID: "D-7f3b", Queued: true, QueuePos: 1},
		{Token: "worker", Kind: TargetRole, Err: "Delegation refused: turn budget exhausted."},
	})
	want := "dispatched: @log-investigator (running, D-7f3a) · @docs-investigator (queued #1, D-7f3b) · @worker (refused: Delegation refused: turn budget exhausted.)"
	if got != want {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement.** Shape:

```go
func (s *Service) Route(ctx context.Context, in RouteInput) []Dispatch {
	if !s.limits().MentionRouter {
		return nil
	}
	rootID := s.rootForSession(ctx, in.SessionID) // extract the lookup subagents.go uses, or accept rootID in RouteInput — pick ONE and document it
	res, err := s.NewResolver(ctx, rootID, in.ProjectID)
	if err != nil {
		return nil // best-effort: routing must never fail the turn
	}
	mentions := ParseMentions(in.Text, res.AllNames())
	forceAsync := len(mentions) > 1
	var out []Dispatch
	for _, m := range mentions {
		switch t := res.Resolve(m.Handle); t.Kind {
		case TargetAgent:
			_, serr := s.SendMessage(ctx, SendInput{
				RootID: rootID, FromHandle: in.FromHandle,
				ToHandle: t.Handle, Body: m.Body, Kind: entity.MessageTell,
			})
			out = append(out, dispatchFor(m, t, nil, serr))
		case TargetRole:
			req := Request{
				ProfileKey: t.Role, Task: m.Body,
				ParentSessionID: in.SessionID, ProjectID: in.ProjectID,
				TriggeredBy: in.TriggeredBy,
			}
			if forceAsync {
				req.Mode, req.DeliverySink = ModeAsync, SinkSession
			}
			r, rerr := s.Run(ctx, req)
			out = append(out, dispatchFor(m, t, r, rerr))
		}
	}
	return out
}
```

Note for the implementer: `SendMessage`'s real input struct is in `mailbox.go` — read it
before writing `SendInput` fields here; the names above are believed-correct but the file
is authoritative. Same for the tell-kind constant (`entity.MessageTell` or the package's
own constant — check `entity/agent_message.go`). A refusal error from `Run` fills
`Dispatch.Err` with `refusal.Message`; any other error is logged at Warn and the Dispatch
carries `Err: "dispatch failed"` without internals.

- [ ] **Step 4: `go test ./internal/agents/delegation/ -run 'TestRoute|TestFormatDispatches' -count=1` — PASS, then whole package.**

---

### Task 4: Turn observer

**Files:**
- Create: `internal/agents/delegation/observer.go`, `observer_test.go`

**Interfaces:**
- Consumes: `event.AgentEvent` (`internal/agents/event` — `Type` of `TextDelta` carries
  `.Text`; `Done` ends a turn), `Service.Route`.
- Produces:

```go
// TurnObserver accumulates streamed text per (session, agent) and routes
// the full turn on Done. It is the ONLY place agent output is scanned
// for mentions — a second scan elsewhere double-dispatches.
type TurnObserver struct { /* mu, buf map[string]*strings.Builder, route func */ }

func NewTurnObserver(route func(sessionID, text string)) *TurnObserver
func (o *TurnObserver) OnEvent(sessionID, agentName string, ev event.AgentEvent)
```

- [ ] **Step 1: Failing test**

```go
func TestObserverRoutesOnceOnDone(t *testing.T) {
	var got []string
	o := NewTurnObserver(func(sid, text string) { got = append(got, sid+"|"+text) })
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "@worker "})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "check auth"})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done}) // no deltas since
	if len(got) != 1 || got[0] != "s1|@worker check auth" {
		t.Fatalf("got %v", got)
	}
}

func TestObserverKeysBySessionAndAgent(t *testing.T) {
	// interleave deltas for ("s1","a") and ("s1","b"); each Done flushes
	// only its own buffer.
}
```

- [ ] **Step 2: FAIL.** **Step 3:** implement with a mutex-guarded map keyed
      `sessionID+"\x00"+agentName`; `Done` with empty buffer is a no-op; buffer cleared on
      flush. Route callback runs on the caller's goroutine — the wiring task decides
      threading.
- [ ] **Step 4: PASS.**

---

### Task 5: Wiring — human path and agent path

**Files:**
- Modify: `internal/pkg/api/server.go` (factory callbacks, ~line 554), `internal/tools/agents/handler.go` (`sendAgent`, ~line 1460)
- Test: `internal/agents/delegation/router_test.go` additions + a handler-level test
  following `internal/tools/agents/stream_test.go` patterns

**Interfaces:**
- Consumes: Tasks 3–4.
- Produces: mentions work end-to-end from both authors.

- [ ] **Step 1:** `server.go` — build the observer once, next to where `channelReg` and the
      factory are built:

```go
	turnObs := delegation.NewTurnObserver(func(sid, text string) {
		// Fire-and-forget: routing must never delay event fan-out.
		go func() {
			ctx := context.Background()
			delegationSvc.RouteAgentTurn(ctx, sid, text) // thin wrapper that fills RouteInput{Author:"agent", FromHandle: handleForSession(sid), ...}
		}()
	})
```

  and add `turnObs.OnEvent(sid, name, ev)` inside BOTH existing callbacks (`OnEvent` and
  `OnExit`'s synthetic Done — server.go:559-577), after `channelReg.DispatchAgentEvent`.
  `RouteAgentTurn` (new, router.go) resolves ProjectID/TriggeredBy/FromHandle from the
  session's delegation row (`FindByChildSession`) or session meta for the leader; the
  feedback line from `FormatDispatches` is delivered back to the authoring agent via the
  existing mailbox (`SendMessage` from handle `"main"` roster-note) — read spec §Feedback:
  it rides on *the next thing that agent receives*, so enqueue it as a system-kind message
  rather than interrupting.

- [ ] **Step 2:** `handler.go` `sendAgent` — after the existing `resetHopsForSession(c, id)`
      line, add the human route:

```go
	go globalDelegation.Route(context.Background(), delegation.RouteInput{
		SessionID: id, ProjectID: sess.Meta.ProjectID,
		FromHandle: entity.LeaderHandle, Author: "human",
		Text: req.Text, TriggeredBy: currentUserID(c),
	})
```

      (Match the actual helpers in that file for project id + user id — both are already
      resolved nearby for the send itself.)

- [ ] **Step 3: Tests.** Package-level: `RouteAgentTurn` on a child session fills
      `FromHandle` with that child's handle (seed a row, assert the SendMessage the router
      makes carries it). Handler-level: POST to the send endpoint with
      `@researcher find x` text → a delegation row exists afterward (fake runner), and the
      message still reached the pool (both things happen, not either).
- [ ] **Step 4: `go test ./internal/agents/... ./internal/tools/agents/... -count=1` — PASS.**

---

### Task 6: Roster block at spawn

**Files:**
- Modify: `internal/agents/delegation/run.go` (`composeTask`, line ~778 and its call site
  at 411), `internal/agents/delegation/format.go`
- Test: `internal/agents/delegation/format_test.go`

**Interfaces:**
- Consumes: `RosterEntry`, `BudgetLine` (format.go), `Resolver` (Task 1).
- Produces: `FormatRosterBlock(roster []RosterEntry, spawnable []string, b BudgetLine) string`;
  `composeTask` gains the block; `FormatInbound` reuses `FormatRosterBlock`'s roster line.

- [ ] **Step 1: Failing test**

```go
func TestRosterBlockAtSpawn(t *testing.T) {
	got := FormatRosterBlock(
		[]RosterEntry{{Handle: "main", Role: "leader", State: "working"},
			{Handle: "code-investigator", Role: "code-investigator", State: "working"}},
		[]string{"log-investigator", "docs-investigator"},
		BudgetLine{TurnsUsed: 6, TurnsMax: 40, Hop: 0, HopMax: 10},
	)
	want := "roster (snapshot at spawn — call list_agents for the current list):\n" +
		"  @main (leader, working) · @code-investigator (code-investigator, working)\n" +
		"spawnable roles: log-investigator, docs-investigator\n" +
		"left: 34/40 turns left · 10/10 hops left\n" +
		"Message a peer with the message op, or open a line with @handle at the start of a line.\n"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRosterBlockOmittedWhenAlone(t *testing.T) {
	// roster of just this agent + no spawnable roles → "" (no empty header)
}
```

- [ ] **Step 2: FAIL.** **Step 3:** implement `FormatRosterBlock`; in `Run`, before
      building `ChildSpec`, fetch `rosterAndBudget(ctx, rootID)` (exists, mailbox.go:325)
      plus the resolver's role keys, and pass the rendered block into `composeTask` as a
      third argument: `composeTask(task, context, rosterBlock)` appending it after the
      context section. Update `composeTask`'s two existing callers/tests accordingly.
- [ ] **Step 4: Package tests PASS.**

---

### Task 7: Prompt + docs

**Files:**
- Modify: `internal/agents/system-prompt/immutable.md`, `docs/guide/agents/sub-agents.md`

- [ ] **Step 1:** Immutable prompt gains a short section under the existing sub-agents
      material: mentions are line-leading `@name task text`; `@` a live handle to talk to
      it, `@` a role key to spawn it; several mentions in one message run in the
      background one at a time; the `dispatched:` line reports what actually started.
- [ ] **Step 2:** User docs: same content with an example transcript, plus the
      `sub_agents_mention_router` setting and what "off" restores. English copy, generic
      names (`@log-investigator check abc.com timeouts between 10:00-11:00`).
- [ ] **Step 3:** `go test ./internal/agents/system-prompt/... -count=1` (a test asserts
      the file parses/loads) — PASS.

## Self-review notes

- Spec resolution order → Task 1 test `PrefersLiveAgentOverRole`. ✔
- Spec "tell never ask" → Task 3 SendInput Kind hard-coded tell. ✔
- Spec "silence for unknown" → router loop has no TargetUnknown arm; corpus test. ✔
- Spec "one observer only" → observer.go doc comment states it; nothing added in run.go. ✔
- Open risk, flagged for the implementer in Task 3: exact `SendInput` field names and the
  tell-kind constant must be read from `mailbox.go` / `entity/agent_message.go`, which are
  newer than this plan.
