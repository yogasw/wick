# Agent Mention Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every agent in a delegation tree a stable address and a mailbox, so agents can ask each other questions and coordinate instead of only receiving one-way tasks.

**Architecture:** One new table (`agent_messages`) and one new column (`agent_delegations.handle`) sit on top of the delegation machinery that already landed. Messages queue FIFO per handle and are drained as ONE batched turn at a turn boundary; a target whose process has exited is resumed via its recorded CLI session. `ask` blocks the sender until a matching reply (or the target's next final turn, auto-promoted to a reply, so a forgetful model can never deadlock the sender). Two brakes: the existing per-root turn/token budget, plus a new hop counter that stops runaway agent-to-agent chatter and resets when a human speaks.

**Tech Stack:** Go 1.2x, GORM (postgres + sqlite dialects only), templ, Svelte 5 runes, Vite, vitest.

**Spec:** [design.md](design.md)

## Global Constraints

- **Never `git commit`.** The user commits. Every task ends at "tests pass". Do not stage, commit, push, or open a PR.
- **UI copy is English.** Every label, placeholder, helper string, and error message.
- Use `abc.com`, `example.com`, generic names in samples and docs.
- **Never edit `*_templ.go`.** Edit the `.templ` source and run `templ generate`.
- **Design system:** Inter (`font-sans`), 8px spacing grid, named Tailwind tokens only (no raw hex, no arbitrary values), a `dark:` counterpart on every colour class, status from the `pos/prog/cau/neg` ramps — green is the accent, never "success".
- **Zerolog:** `l := log.With().Str("component", "x").Logger()`, then `l.Debug()...`. Never `log.Debug()` directly.
- **`safeexec`, never `os/exec`.** `TestNoDirectOSExec` enforces it.
- Dialects in play are **postgres and sqlite only**. No MySQL-only DDL.
- Run Go tests with `-count=1` — the cache hides real failures.
- **No dead knobs.** Every config key added here must be read by code in the same task that adds it.

---

## File Structure

**Created:**

| Path | Responsibility |
|---|---|
| `internal/entity/agent_message.go` | `AgentMessage` struct + status/kind constants. |
| `internal/agents/delegation/handle.go` | `AllocateHandle` — the pure dedup rule. Nothing else. |
| `internal/agents/delegation/handle_test.go` | Handle dedup + reserved-name tests. |
| `internal/agents/delegation/mailbox.go` | Enqueue / drain / ask-wait / auto-reply. The runtime. |
| `internal/agents/delegation/mailbox_test.go` | FIFO, batching, timeout, auto-reply, inbox cap. |
| `internal/agents/delegation/mailbox_repo.go` | GORM queries for `agent_messages`. |
| `internal/agents/delegation/mailbox_repo_test.go` | Repo round-trips against sqlite. |
| `internal/agents/delegation/format.go` | `FormatInbound` — roster + budget footer rendering. |
| `internal/agents/delegation/format_test.go` | Exact-output tests. |
| `internal/agents/delegation/mention.go` | `ParseMentions` — strict `@handle` scanner. |
| `internal/agents/delegation/mention_test.go` | False-positive corpus (`@media`, email, fences). |
| `fe/agents/conversation/src/lib/components/MessageThread.svelte` | Rail conversation view. |

**Modified:**

| Path | Change |
|---|---|
| `internal/entity/agent_delegation.go` | `+ Handle`, `+ HopCount` columns. |
| `internal/pkg/postgres/migrate.go` | Register `&entity.AgentMessage{}`; drop-and-recreate composite unique `(root_id, handle)`. |
| `internal/agents/delegation/governor.go` | `+ Limits.MaxHops`, `+ AdmitMessage`. |
| `internal/agents/delegation/acl.go` | `CanInterrupt` gains the same-root agent case. |
| `internal/agents/delegation/run.go` | Allocate a handle at spawn; `+ Waker` interface on `Service`. |
| `internal/agents/config/general.go` | `+ SubAgentsMaxHops`, `SubAgentsAskTimeoutMin`, `SubAgentsInboxCap`. |
| `internal/pkg/api/delegation_limits.go` | Read the three new keys. |
| `internal/connectors/sub-agents/connector.go` | `+ message`, `reply`, `stop` ops in a new `Messaging` category. |
| `internal/connectors/sub-agents/handlers.go` | Handlers for the three ops. |
| `internal/tools/agents/subagents.go` | `+ GET/POST` message endpoints, `+ hop bump`. |
| `internal/tools/agents/api_conversation.go` | Reset hop count when a human posts a turn. |
| `internal/agents/system-prompt/immutable.md` | Mention section. |
| `fe/agents/conversation/src/lib/components/SubAgentPanel.svelte` | Inbox badges, stop, `+10 hop`. |
| `docs/guide/agents/sub-agents.md` | User-facing mention docs. |

---

### Task 1: Handle allocation

**Files:**
- Create: `internal/agents/delegation/handle.go`, `internal/agents/delegation/handle_test.go`
- Modify: `internal/entity/agent_delegation.go`, `internal/pkg/postgres/migrate.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `delegation.AllocateHandle(profileKey string, taken []string) string`; `entity.AgentDelegation.Handle string`; `entity.LeaderHandle = "main"`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agents/delegation/handle_test.go
package delegation

import "testing"

func TestAllocateHandle(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		taken   []string
		want    string
	}{
		{"first of a role keeps the bare key", "reviewer", nil, "reviewer"},
		{"second gets -2", "reviewer", []string{"reviewer"}, "reviewer-2"},
		{"gap is not reused", "reviewer", []string{"reviewer", "reviewer-3"}, "reviewer-2"},
		{"leader name is reserved", "main", nil, "main-2"},
		{"uppercase folds down", "Code-Reviewer", nil, "code-reviewer"},
		{"spaces become dashes", "code reviewer", nil, "code-reviewer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AllocateHandle(tc.profile, tc.taken); got != tc.want {
				t.Fatalf("AllocateHandle(%q, %v) = %q, want %q", tc.profile, tc.taken, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestAllocateHandle -count=1`
Expected: FAIL — `undefined: AllocateHandle`

- [ ] **Step 3: Write minimal implementation**

```go
// internal/agents/delegation/handle.go
package delegation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// Handles address one INSTANCE, not a role: two sub-agents spawned from
// the same profile must be separately reachable, otherwise "@reviewer"
// is ambiguous the moment a second reviewer exists.

var handleUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// AllocateHandle returns a handle for a new instance of profileKey that
// does not collide with taken.
//
// The leader's handle is reserved: a profile literally named "main" must
// not be able to impersonate the conversation owner.
func AllocateHandle(profileKey string, taken []string) string {
	base := handleUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(profileKey)), "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "agent"
	}
	used := make(map[string]bool, len(taken)+1)
	used[entity.LeaderHandle] = true
	for _, t := range taken {
		used[t] = true
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s-%d", base, n)
		if !used[cand] {
			return cand
		}
	}
}
```

```go
// internal/entity/agent_delegation.go — add to the AgentDelegation struct
	// Handle is this instance's address inside its tree ("reviewer-2").
	// Unique per RootID; allocated at spawn and never reused.
	Handle string `gorm:"type:varchar(64);not null;default:''" json:"handle"`
	// HopCount is the number of consecutive agent-to-agent messages in
	// this tree since a human last spoke. Stored on the ROOT row only.
	HopCount int `gorm:"not null;default:0" json:"hop_count"`
```

```go
// internal/entity/agent_delegation.go — add near the status constants
// LeaderHandle is the address of the conversation owner. Reserved so a
// sub-agent cannot claim it.
const LeaderHandle = "main"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agents/delegation/ -run TestAllocateHandle -count=1`
Expected: PASS

- [ ] **Step 5: Add the composite unique index**

```go
// internal/pkg/postgres/migrate.go — beside the existing agent_profiles
// index surgery, before AutoMigrate runs.
//
// Valid on both postgres and sqlite.
if err := db.Exec(
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_delegations_root_handle
	 ON agent_delegations(root_id, handle)`).Error; err != nil {
	return fmt.Errorf("agent_delegations handle index: %w", err)
}
```

- [ ] **Step 6: Verify the whole package still builds and passes**

Run: `go build ./... && go test ./internal/agents/delegation/ ./internal/pkg/postgres/ -count=1`
Expected: PASS

---

### Task 2: `agent_messages` entity + repo

**Files:**
- Create: `internal/entity/agent_message.go`, `internal/agents/delegation/mailbox_repo.go`, `internal/agents/delegation/mailbox_repo_test.go`
- Modify: `internal/pkg/postgres/migrate.go`

**Interfaces:**
- Consumes: `entity.AgentDelegation.Handle` (Task 1).
- Produces: `entity.AgentMessage`; `(*Repo).EnqueueMessage(ctx, *entity.AgentMessage) error`; `(*Repo).DrainInbox(ctx, rootID, handle string, max int) ([]entity.AgentMessage, error)`; `(*Repo).CountQueued(ctx, rootID, handle string) (int64, error)`; `(*Repo).MarkAnswered(ctx, askID, replyID string) error`; `(*Repo).FindReply(ctx, askID string) (*entity.AgentMessage, error)`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agents/delegation/mailbox_repo_test.go
package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.AgentMessage{}, &entity.AgentDelegation{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewRepo(db)
}

func msg(root, from, to, body, kind string) *entity.AgentMessage {
	return &entity.AgentMessage{
		RootID: root, FromHandle: from, ToHandle: to,
		Body: body, Kind: kind, Status: entity.MessageQueued,
		CreatedAt: time.Now(),
	}
}

func TestDrainInboxIsFIFOAndBatched(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for _, b := range []string{"one", "two", "three"} {
		if err := r.EnqueueMessage(ctx, msg("root1", "main", "reviewer", b, entity.MessageTell)); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		time.Sleep(time.Millisecond) // distinct created_at ordering
	}
	got, err := r.DrainInbox(ctx, "root1", "reviewer", 2)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("batch cap ignored: got %d messages, want 2", len(got))
	}
	if got[0].Body != "one" || got[1].Body != "two" {
		t.Fatalf("not FIFO: %q, %q", got[0].Body, got[1].Body)
	}
	// Drained messages must not come back a second time.
	again, err := r.DrainInbox(ctx, "root1", "reviewer", 10)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(again) != 1 || again[0].Body != "three" {
		t.Fatalf("redelivery: got %d messages, want only \"three\"", len(again))
	}
}

func TestCountQueuedIgnoresDelivered(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := r.EnqueueMessage(ctx, msg("root2", "main", "worker", "x", entity.MessageTell)); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if _, err := r.DrainInbox(ctx, "root2", "worker", 2); err != nil {
		t.Fatalf("drain: %v", err)
	}
	n, err := r.CountQueued(ctx, "root2", "worker")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("CountQueued = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestDrainInbox -count=1`
Expected: FAIL — `undefined: entity.AgentMessage`

- [ ] **Step 3: Write the entity**

```go
// internal/entity/agent_message.go
package entity

import "time"

// Message kinds. A reply is stored as its own row rather than a column on
// the ask, so the thread reads as a conversation in creation order.
const (
	MessageAsk   = "ask"
	MessageTell  = "tell"
	MessageReply = "reply"
)

// Message statuses.
const (
	MessageQueued    = "queued"
	MessageDelivered = "delivered"
	MessageAnswered  = "answered"
	MessageDropped   = "dropped"
)

// AgentMessage is one message between two agents inside a delegation
// tree.
//
// RootID scopes addressing: a handle only means anything inside its own
// tree, so a message can never reach an agent in someone else's
// conversation.
//
// FromHandle is written by the SERVER from the calling session, never
// taken from model input — a model that could name its own sender could
// impersonate the leader and inherit its authority.
type AgentMessage struct {
	ID         string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	RootID     string `gorm:"type:varchar(64);not null;index:idx_agent_messages_inbox,priority:1" json:"root_id"`
	FromHandle string `gorm:"type:varchar(64);not null" json:"from_handle"`
	ToHandle   string `gorm:"type:varchar(64);not null;index:idx_agent_messages_inbox,priority:2" json:"to_handle"`
	Body       string `gorm:"type:text;not null" json:"body"`
	Kind       string `gorm:"type:varchar(16);not null" json:"kind"`
	// ReplyTo points a reply at the ask it answers.
	ReplyTo string `gorm:"type:varchar(64);index" json:"reply_to,omitempty"`
	// AutoReply marks a reply wick synthesised from the recipient's final
	// turn because it never called reply explicitly. Surfaced so a reader
	// can tell a deliberate answer from a salvaged one.
	AutoReply bool   `gorm:"not null;default:false" json:"auto_reply,omitempty"`
	Status    string `gorm:"type:varchar(16);not null;index:idx_agent_messages_inbox,priority:3" json:"status"`
	// Hop is the hop counter value when this message was sent — kept for
	// audit, while the live counter lives on the root delegation row.
	Hop         int        `gorm:"not null;default:0" json:"hop"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}
```

- [ ] **Step 4: Write the repo**

```go
// internal/agents/delegation/mailbox_repo.go
package delegation

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// EnqueueMessage stores one message as queued.
func (r *Repo) EnqueueMessage(ctx context.Context, m *entity.AgentMessage) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	if m.Status == "" {
		m.Status = entity.MessageQueued
	}
	return r.db.WithContext(ctx).Create(m).Error
}

// DrainInbox takes up to max queued messages for one handle, oldest
// first, and marks them delivered in the same transaction.
//
// Claim-then-return (not read-then-mark) so two concurrent drains of the
// same inbox cannot hand the same message to the recipient twice.
func (r *Repo) DrainInbox(ctx context.Context, rootID, handle string, max int) ([]entity.AgentMessage, error) {
	if max <= 0 {
		max = 10
	}
	var out []entity.AgentMessage
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("root_id = ? AND to_handle = ? AND status = ?",
			rootID, handle, entity.MessageQueued).
			Order("created_at asc").Limit(max).Find(&out).Error; err != nil {
			return err
		}
		if len(out) == 0 {
			return nil
		}
		ids := make([]string, 0, len(out))
		for i := range out {
			ids = append(ids, out[i].ID)
		}
		now := time.Now()
		return tx.Model(&entity.AgentMessage{}).Where("id IN ?", ids).
			Updates(map[string]any{"status": entity.MessageDelivered, "delivered_at": now}).Error
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CountQueued reports how far behind an inbox is, for the backpressure cap.
func (r *Repo) CountQueued(ctx context.Context, rootID, handle string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&entity.AgentMessage{}).
		Where("root_id = ? AND to_handle = ? AND status = ?",
			rootID, handle, entity.MessageQueued).Count(&n).Error
	return n, err
}

// MarkAnswered closes an ask once its reply exists.
func (r *Repo) MarkAnswered(ctx context.Context, askID, replyID string) error {
	return r.db.WithContext(ctx).Model(&entity.AgentMessage{}).
		Where("id = ?", askID).
		Updates(map[string]any{"status": entity.MessageAnswered, "reply_to": replyID}).Error
}

// FindReply returns the reply to an ask, or nil when none exists yet.
func (r *Repo) FindReply(ctx context.Context, askID string) (*entity.AgentMessage, error) {
	var m entity.AgentMessage
	err := r.db.WithContext(ctx).
		Where("reply_to = ? AND kind = ?", askID, entity.MessageReply).
		Order("created_at asc").First(&m).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// ListThread returns every message in a tree, oldest first, for the UI.
func (r *Repo) ListThread(ctx context.Context, rootID string, limit int) ([]entity.AgentMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	var out []entity.AgentMessage
	err := r.db.WithContext(ctx).Where("root_id = ?", rootID).
		Order("created_at asc").Limit(limit).Find(&out).Error
	return out, err
}

// TakenHandles lists the handles already allocated in a tree.
func (r *Repo) TakenHandles(ctx context.Context, rootID string) ([]string, error) {
	var out []string
	err := r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("root_id = ? AND handle <> ''", rootID).
		Pluck("handle", &out).Error
	return out, err
}
```

- [ ] **Step 5: Register the table**

```go
// internal/pkg/postgres/migrate.go — in the AutoMigrate list, after &entity.AgentTask{}
		&entity.AgentMessage{},
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/agents/delegation/ -run 'TestDrainInbox|TestCountQueued' -count=1`
Expected: PASS

---

### Task 3: Hop counter + governor admission

**Files:**
- Modify: `internal/agents/delegation/governor.go`, `internal/agents/config/general.go`, `internal/pkg/api/delegation_limits.go`
- Test: `internal/agents/delegation/governor_test.go`

**Interfaces:**
- Consumes: `Limits` (existing), `entity.AgentDelegation.HopCount` (Task 1).
- Produces: `Limits.MaxHops int`; `(*Repo).BumpHop(ctx, rootID string) (int, error)`; `(*Repo).ResetHops(ctx, rootID string) error`; `AdmitMessage(lim Limits, hop int) error`; config keys `sub_agents_max_hops`, `sub_agents_ask_timeout_min`, `sub_agents_inbox_cap`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agents/delegation/governor_test.go — append
func TestAdmitMessage(t *testing.T) {
	lim := Limits{MaxHops: 3}
	if err := AdmitMessage(lim, 2); err != nil {
		t.Fatalf("hop 2 of 3 must be allowed: %v", err)
	}
	err := AdmitMessage(lim, 3)
	if err == nil {
		t.Fatal("hop 3 of 3 must be refused")
	}
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Reason != RefusedHops {
		t.Fatalf("want RefusedHops refusal, got %T %v", err, err)
	}
	// The message must tell the agent what to do instead, not just say no.
	if !strings.Contains(ref.Message, "report") {
		t.Fatalf("refusal gives no next step: %q", ref.Message)
	}
}

func TestAdmitMessageRespectsKillSwitch(t *testing.T) {
	if err := AdmitMessage(Limits{Disabled: true, MaxHops: 10}, 0); err == nil {
		t.Fatal("kill-switch must refuse messages too, not only spawns")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestAdmitMessage -count=1`
Expected: FAIL — `undefined: AdmitMessage`

- [ ] **Step 3: Implement the governor half**

```go
// internal/agents/delegation/governor.go — add beside the other refusal reasons
	// RefusedHops means agents have been talking to each other for too
	// many consecutive turns without a human in the loop.
	RefusedHops = "hops"

// DefaultMaxHops bounds consecutive agent-to-agent messages between human
// turns. Ten is enough for a real exchange (ask, clarify, answer, confirm)
// and short enough that a loop costs a few turns rather than a budget.
const DefaultMaxHops = 10

// AdmitMessage decides whether one more agent-to-agent message may be
// sent, given the tree's current hop count.
//
// Hitting the cap stops MESSAGES, not the agents: every instance stays
// addressable and its work stands, so a human can top the budget up and
// the conversation continues where it left off.
func AdmitMessage(lim Limits, hop int) error {
	lim = lim.normalize()
	if lim.Disabled {
		return &Refusal{
			Reason:  RefusedDisabled,
			Message: "Sub-agent delegation is currently disabled by an administrator.",
		}
	}
	if hop >= lim.MaxHops {
		return &Refusal{
			Reason: RefusedHops,
			Message: fmt.Sprintf(
				"Agents have exchanged %d messages since the last human turn (limit %d). "+
					"Stop messaging, summarise what you have, and report back to the user.",
				hop, lim.MaxHops),
		}
	}
	return nil
}
```

```go
// internal/agents/delegation/governor.go — add to Limits
	// MaxHops bounds consecutive agent-to-agent messages between human
	// turns. The brake that a turn budget alone does not provide: two
	// agents can chat cheaply for a long time before turns run out.
	MaxHops int
```

```go
// internal/agents/delegation/governor.go — inside normalize(), beside the others
	if l.MaxHops <= 0 {
		l.MaxHops = DefaultMaxHops
	}
```

```go
// internal/agents/delegation/governor.go — add to DefaultLimits()
		MaxHops: DefaultMaxHops,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agents/delegation/ -run TestAdmitMessage -count=1`
Expected: PASS

- [ ] **Step 5: Add the counter methods**

```go
// internal/agents/delegation/mailbox_repo.go — append

// BumpHop increments the tree's hop counter and returns the value BEFORE
// the increment, which is the hop this message occupies.
func (r *Repo) BumpHop(ctx context.Context, rootID string) (int, error) {
	var hop int
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var root entity.AgentDelegation
		if err := tx.Where("id = ?", rootID).First(&root).Error; err != nil {
			return err
		}
		hop = root.HopCount
		return tx.Model(&entity.AgentDelegation{}).Where("id = ?", rootID).
			Update("hop_count", hop+1).Error
	})
	return hop, err
}

// ResetHops clears the counter. Called when a HUMAN posts a turn — the
// only actor allowed to reset it, since a leader that could reset its own
// limit would not be limited.
func (r *Repo) ResetHops(ctx context.Context, rootID string) error {
	return r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ?", rootID).Update("hop_count", 0).Error
}
```

- [ ] **Step 6: Wire the three config keys**

```go
// internal/agents/config/general.go — add to GeneralConfig, in the Sub-agents group
	SubAgentsMaxHops       int `wick:"number;group=Sub-agents;desc=How many messages agents may exchange with each other between human turns. Guards against two agents talking in a loop. Reset whenever a person sends a message. Default: 10."`
	SubAgentsAskTimeoutMin int `wick:"number;group=Sub-agents;desc=How many minutes an agent waits for an answer to a blocking ask before giving up. The question stays in the recipient's inbox either way. Default: 10."`
	SubAgentsInboxCap      int `wick:"number;group=Sub-agents;desc=How many undelivered messages one agent may have waiting before senders are refused. Stops a fast agent from burying a slow one. Default: 20."`
```

```go
// internal/agents/config/general.go — add to DefaultGeneralConfig()
		SubAgentsMaxHops:       delegationDefaultMaxHops,
		SubAgentsAskTimeoutMin: 10,
		SubAgentsInboxCap:      20,
```

Declare the shared default next to the other sub-agent defaults in that file so the config package does not import `delegation`:

```go
// internal/agents/config/general.go — beside the other sub-agent constants
// delegationDefaultMaxHops mirrors delegation.DefaultMaxHops. Duplicated
// rather than imported because config must not depend on the delegation
// package; the value is asserted equal by a test in delegation.
const delegationDefaultMaxHops = 10
```

```go
// internal/pkg/api/delegation_limits.go — inside current(), beside the others
	lim.MaxHops = intOr(p.cfg.GetOwned("agents", "sub_agents_max_hops"), def.SubAgentsMaxHops)
```

- [ ] **Step 7: Pin the duplicated default with a test**

```go
// internal/agents/delegation/governor_test.go — append
func TestConfigDefaultMatchesGovernorDefault(t *testing.T) {
	if config.DefaultGeneralConfig().SubAgentsMaxHops != DefaultMaxHops {
		t.Fatalf("config default %d drifted from governor default %d",
			config.DefaultGeneralConfig().SubAgentsMaxHops, DefaultMaxHops)
	}
}
```

- [ ] **Step 8: Run the suite**

Run: `go test ./internal/agents/delegation/ ./internal/agents/config/ ./internal/pkg/api/ -count=1`
Expected: PASS

---

### Task 4: Roster + budget footer

**Files:**
- Create: `internal/agents/delegation/format.go`, `internal/agents/delegation/format_test.go`

**Interfaces:**
- Consumes: `entity.AgentMessage` (Task 2).
- Produces: `RosterEntry{Handle, Role, State string}`; `BudgetLine{TurnsUsed, TurnsMax, TokensUsed, TokensMax, Hop, HopMax int}`; `FormatInbound(msgs []entity.AgentMessage, roster []RosterEntry, b BudgetLine) string`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agents/delegation/format_test.go
package delegation

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestFormatInboundCarriesSenderRosterAndBudget(t *testing.T) {
	msgs := []entity.AgentMessage{
		{FromHandle: "reviewer", Body: "2 of 5 files done.", Kind: entity.MessageTell},
	}
	roster := []RosterEntry{
		{Handle: "main", Role: "leader", State: "working"},
		{Handle: "reviewer", Role: "code-reviewer", State: "working"},
	}
	out := FormatInbound(msgs, roster, BudgetLine{
		TurnsUsed: 28, TurnsMax: 40, TokensUsed: 340_000, TokensMax: 1_000_000, Hop: 7, HopMax: 10,
	})

	if !strings.Contains(out, "from @reviewer") {
		t.Fatalf("sender missing:\n%s", out)
	}
	if !strings.Contains(out, "2 of 5 files done.") {
		t.Fatalf("body missing:\n%s", out)
	}
	if !strings.Contains(out, "@main") || !strings.Contains(out, "@reviewer (code-reviewer)") {
		t.Fatalf("roster missing:\n%s", out)
	}
	// Remaining, not consumed: an agent budgets against what is LEFT.
	if !strings.Contains(out, "12/40 turns left") {
		t.Fatalf("turn remainder missing or wrong:\n%s", out)
	}
	if !strings.Contains(out, "3/10 hops left") {
		t.Fatalf("hop remainder missing or wrong:\n%s", out)
	}
}

func TestFormatInboundBatchesEveryMessage(t *testing.T) {
	msgs := []entity.AgentMessage{
		{FromHandle: "main", Body: "first", Kind: entity.MessageTell},
		{FromHandle: "worker-2", Body: "second", Kind: entity.MessageAsk},
	}
	out := FormatInbound(msgs, nil, BudgetLine{TurnsMax: 40, HopMax: 10})
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("batched messages dropped:\n%s", out)
	}
	// An ask must be marked, or the recipient does not know a reply is owed.
	if !strings.Contains(out, "asks") {
		t.Fatalf("ask not marked:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestFormatInbound -count=1`
Expected: FAIL — `undefined: FormatInbound`

- [ ] **Step 3: Write the implementation**

```go
// internal/agents/delegation/format.go
package delegation

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// RosterEntry is one addressable agent, as the recipient should see it.
type RosterEntry struct {
	Handle string
	Role   string
	State  string // working | idle | done
}

// BudgetLine is what is left of the tree's allowances.
type BudgetLine struct {
	TurnsUsed, TurnsMax   int
	TokensUsed, TokensMax int
	Hop, HopMax           int
}

// FormatInbound renders a batch of queued messages as ONE turn.
//
// The roster and the budget ride on every delivery instead of being
// injected once at spawn: the roster changes as instances appear and
// finish, and an agent that cannot see what is left will happily start an
// exchange it has no budget to finish.
func FormatInbound(msgs []entity.AgentMessage, roster []RosterEntry, b BudgetLine) string {
	var sb strings.Builder
	for _, m := range msgs {
		verb := "says"
		switch m.Kind {
		case entity.MessageAsk:
			verb = "asks (a reply is expected)"
		case entity.MessageReply:
			verb = "replies"
		}
		fmt.Fprintf(&sb, "── from @%s %s ──\n%s\n\n", m.FromHandle, verb, strings.TrimSpace(m.Body))
	}
	if len(roster) > 0 {
		parts := make([]string, 0, len(roster))
		for _, r := range roster {
			parts = append(parts, fmt.Sprintf("@%s (%s, %s)", r.Handle, r.Role, r.State))
		}
		fmt.Fprintf(&sb, "roster: %s\n", strings.Join(parts, " · "))
	}
	sb.WriteString("left: " + b.String() + "\n")
	return sb.String()
}

// String renders remaining allowances. Remaining rather than consumed:
// "28/40 used" needs arithmetic before it changes a decision, and the
// decision is always "can I afford one more round?".
func (b BudgetLine) String() string {
	parts := []string{
		fmt.Sprintf("%d/%d turns left", max(b.TurnsMax-b.TurnsUsed, 0), b.TurnsMax),
		fmt.Sprintf("%d/%d hops left", max(b.HopMax-b.Hop, 0), b.HopMax),
	}
	if b.TokensMax > 0 {
		parts = append(parts, fmt.Sprintf("%dk/%dk tokens left",
			max(b.TokensMax-b.TokensUsed, 0)/1000, b.TokensMax/1000))
	}
	return strings.Join(parts, " · ")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agents/delegation/ -run TestFormatInbound -count=1`
Expected: PASS

---

### Task 5: Strict `@handle` parser

**Files:**
- Create: `internal/agents/delegation/mention.go`, `internal/agents/delegation/mention_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ParseMentions(text string, roster []string) []Mention` where `Mention{Handle, Body string}`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agents/delegation/mention_test.go
package delegation

import "testing"

func TestParseMentionsAcceptsLineLeadingKnownHandles(t *testing.T) {
	roster := []string{"main", "reviewer"}
	got := ParseMentions("@reviewer can you look at auth.go?\nthanks", roster)
	if len(got) != 1 {
		t.Fatalf("want 1 mention, got %d: %+v", len(got), got)
	}
	if got[0].Handle != "reviewer" {
		t.Fatalf("handle = %q", got[0].Handle)
	}
	if got[0].Body != "can you look at auth.go?" {
		t.Fatalf("body = %q", got[0].Body)
	}
}

// Every entry here appears in ordinary agent output. A false positive is
// not cosmetic: it spawns an agent and spends tokens because the model
// happened to write an email address.
func TestParseMentionsIgnoresLookalikes(t *testing.T) {
	roster := []string{"main", "reviewer", "media"}
	cases := map[string]string{
		"css at-rule":     "@media (min-width: 40rem) { .a { color: red } }",
		"email":           "ping ops@abc.com about it",
		"npm scope":       "@scope/pkg is the dependency",
		"mid-line":        "I told @reviewer already",
		"unknown handle":  "@nobody are you there?",
		"inside a fence":  "```\n@reviewer look here\n```",
		"decorator":       "@ts-ignore",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ParseMentions(in, roster); len(got) != 0 {
				t.Fatalf("false positive on %s: %+v", name, got)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestParseMentions -count=1`
Expected: FAIL — `undefined: ParseMentions`

- [ ] **Step 3: Write the implementation**

```go
// internal/agents/delegation/mention.go
package delegation

import "strings"

// Mention is one @handle directive found in an agent's text.
type Mention struct {
	Handle string
	Body   string
}

// ParseMentions finds mentions in an agent's final text.
//
// Deliberately strict: line-leading only, the handle must already exist
// in the tree's roster, and fenced code is skipped. Agent output is full
// of @ tokens that are not mentions — @media, @ts-ignore, @scope/pkg,
// email addresses. When a candidate is not certain, it stays plain text:
// a missed mention costs one clarifying turn, a false one spawns work
// nobody asked for.
func ParseMentions(text string, roster []string) []Mention {
	known := make(map[string]bool, len(roster))
	for _, h := range roster {
		known[h] = true
	}
	var out []Mention
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "@") {
			continue
		}
		rest := trimmed[1:]
		cut := strings.IndexAny(rest, " \t")
		if cut <= 0 {
			continue
		}
		handle, body := rest[:cut], strings.TrimSpace(rest[cut:])
		if body == "" || !known[handle] {
			continue
		}
		out = append(out, Mention{Handle: handle, Body: body})
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agents/delegation/ -run TestParseMentions -count=1`
Expected: PASS

---

### Task 6: Mailbox runtime — send, deliver, ask-wait, auto-reply

**Files:**
- Create: `internal/agents/delegation/mailbox.go`, `internal/agents/delegation/mailbox_test.go`
- Modify: `internal/agents/delegation/run.go`

**Interfaces:**
- Consumes: `Repo` mailbox methods (Task 2), `AdmitMessage` + `BumpHop` (Task 3), `FormatInbound` (Task 4), existing `Steerer`, `Runner`, `Limits`.
- Produces: `Waker` interface; `(*Service).SendMessage(ctx, SendInput) (*SendResult, error)`; `(*Service).Reply(ctx, askID, fromHandle, body string) error`; `SendInput{RootID, FromHandle, ToHandle, Body, Kind string}`; `SendResult{MessageID, Status, Reply, Note string}`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agents/delegation/mailbox_test.go
package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

type fakeWaker struct{ woke []string }

func (f *fakeWaker) WakeChild(_ context.Context, childSessionID, _ string) error {
	f.woke = append(f.woke, childSessionID)
	return nil
}

func seedTree(t *testing.T, r *Repo) {
	t.Helper()
	ctx := context.Background()
	rows := []entity.AgentDelegation{
		{ID: "root1", RootID: "root1", Handle: "main", Status: entity.DelegationRunning,
			ChildSessionID: "sess-main", ProfileKey: "leader"},
		{ID: "d2", RootID: "root1", Handle: "reviewer", Status: entity.DelegationRunning,
			ChildSessionID: "sess-rev", ProfileKey: "code-reviewer"},
	}
	for i := range rows {
		if err := r.db.WithContext(ctx).Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestSendTellDoesNotBlockAndBumpsHop(t *testing.T) {
	r := newTestRepo(t)
	seedTree(t, r)
	svc := &Service{Repo: r, Limits: Limits{MaxHops: 10}, Waker: &fakeWaker{}}

	res, err := svc.SendMessage(context.Background(), SendInput{
		RootID: "root1", FromHandle: "main", ToHandle: "reviewer",
		Body: "how is it going?", Kind: entity.MessageTell,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Status != "sent" {
		t.Fatalf("tell must not wait, status = %q", res.Status)
	}
	var root entity.AgentDelegation
	if err := r.db.First(&root, "id = ?", "root1").Error; err != nil {
		t.Fatalf("reload root: %v", err)
	}
	if root.HopCount != 1 {
		t.Fatalf("hop not counted: %d", root.HopCount)
	}
}

func TestSendToUnknownHandleIsRefused(t *testing.T) {
	r := newTestRepo(t)
	seedTree(t, r)
	svc := &Service{Repo: r, Limits: Limits{MaxHops: 10}, Waker: &fakeWaker{}}

	_, err := svc.SendMessage(context.Background(), SendInput{
		RootID: "root1", FromHandle: "main", ToHandle: "ghost",
		Body: "hello", Kind: entity.MessageTell,
	})
	if err == nil {
		t.Fatal("unknown handle must be refused")
	}
}

func TestSendRefusedWhenInboxFull(t *testing.T) {
	r := newTestRepo(t)
	seedTree(t, r)
	svc := &Service{Repo: r, Limits: Limits{MaxHops: 100}, InboxCap: 2, Waker: &fakeWaker{}}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := svc.SendMessage(ctx, SendInput{
			RootID: "root1", FromHandle: "main", ToHandle: "reviewer",
			Body: "x", Kind: entity.MessageTell,
		}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if _, err := svc.SendMessage(ctx, SendInput{
		RootID: "root1", FromHandle: "main", ToHandle: "reviewer",
		Body: "one too many", Kind: entity.MessageTell,
	}); err == nil {
		t.Fatal("inbox cap must refuse the third message")
	}
}

func TestAskReturnsTheReply(t *testing.T) {
	r := newTestRepo(t)
	seedTree(t, r)
	svc := &Service{Repo: r, Limits: Limits{MaxHops: 10}, AskTimeout: 2 * time.Second, Waker: &fakeWaker{}}
	ctx := context.Background()

	done := make(chan *SendResult, 1)
	go func() {
		res, err := svc.SendMessage(ctx, SendInput{
			RootID: "root1", FromHandle: "main", ToHandle: "reviewer",
			Body: "ready?", Kind: entity.MessageAsk,
		})
		if err != nil {
			t.Errorf("ask: %v", err)
			done <- nil
			return
		}
		done <- res
	}()

	// The recipient answers a moment later.
	var ask entity.AgentMessage
	for i := 0; i < 100; i++ {
		if err := r.db.Where("kind = ?", entity.MessageAsk).First(&ask).Error; err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ask.ID == "" {
		t.Fatal("ask row never appeared")
	}
	if err := svc.Reply(ctx, ask.ID, "reviewer", "yes, ready"); err != nil {
		t.Fatalf("reply: %v", err)
	}

	select {
	case res := <-done:
		if res == nil || res.Reply != "yes, ready" {
			t.Fatalf("ask did not receive the reply: %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ask never returned")
	}
}

func TestAskTimesOutButKeepsTheQuestion(t *testing.T) {
	r := newTestRepo(t)
	seedTree(t, r)
	svc := &Service{Repo: r, Limits: Limits{MaxHops: 10}, AskTimeout: 150 * time.Millisecond, Waker: &fakeWaker{}}

	res, err := svc.SendMessage(context.Background(), SendInput{
		RootID: "root1", FromHandle: "main", ToHandle: "reviewer",
		Body: "still there?", Kind: entity.MessageAsk,
	})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if res.Status != "timeout" {
		t.Fatalf("status = %q, want timeout", res.Status)
	}
	n, err := svc.Repo.CountQueued(context.Background(), "root1", "reviewer")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("timeout dropped the question: %d queued", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run 'TestSend|TestAsk' -count=1`
Expected: FAIL — `undefined: SendInput`

- [ ] **Step 3: Write the runtime**

```go
// internal/agents/delegation/mailbox.go
package delegation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/entity"
)

// defaultAskTimeout bounds how long a blocking ask waits. Timing out
// stops the WAIT, never the question: the message stays queued so the
// recipient still answers it, just not into a caller that is still there.
const defaultAskTimeout = 10 * time.Minute

// defaultInboxCap is the backpressure limit per handle.
const defaultInboxCap = 20

// askPollInterval is how often a waiting ask checks for its reply. A poll
// rather than a subscription because the reply may be written by another
// process (the recipient's own turn), and the wait is minutes-scale.
const askPollInterval = 200 * time.Millisecond

// Waker starts or resumes a sub-agent so it can read its inbox.
// Implemented by the pool wiring; an interface keeps this package free of
// process handling.
type Waker interface {
	// WakeChild resumes an exited child from its recorded CLI session, or
	// no-ops when it is already running.
	WakeChild(ctx context.Context, childSessionID, agentName string) error
}

// SendInput is one agent-to-agent message.
type SendInput struct {
	RootID     string
	FromHandle string // set by the caller from the SESSION, never from model input
	ToHandle   string
	Body       string
	Kind       string // ask | tell
}

// SendResult is what the sender gets back.
type SendResult struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"` // sent | answered | timeout
	Reply     string `json:"reply,omitempty"`
	Note      string `json:"note,omitempty"`
}

// ErrUnknownHandle means the address does not exist in this tree.
var ErrUnknownHandle = errors.New("unknown handle")

// SendMessage queues a message and, for an ask, waits for its reply.
func (s *Service) SendMessage(ctx context.Context, in SendInput) (*SendResult, error) {
	target, err := s.Repo.FindByHandle(ctx, in.RootID, in.ToHandle)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("%w: @%s is not in this conversation", ErrUnknownHandle, in.ToHandle)
	}
	if entity.IsTerminalDelegationStatus(target.Status) && target.Status != entity.DelegationDone {
		return nil, fmt.Errorf("@%s stopped (%s) and cannot take messages", in.ToHandle, target.Status)
	}

	cap := s.InboxCap
	if cap <= 0 {
		cap = defaultInboxCap
	}
	queued, err := s.Repo.CountQueued(ctx, in.RootID, in.ToHandle)
	if err != nil {
		return nil, err
	}
	if queued >= int64(cap) {
		return nil, fmt.Errorf("@%s already has %d unread messages — wait for it to catch up", in.ToHandle, queued)
	}

	hop, err := s.Repo.BumpHop(ctx, in.RootID)
	if err != nil {
		return nil, err
	}
	if err := AdmitMessage(s.limits(), hop); err != nil {
		return nil, err
	}

	m := &entity.AgentMessage{
		ID: uuid.NewString(), RootID: in.RootID,
		FromHandle: in.FromHandle, ToHandle: in.ToHandle,
		Body: in.Body, Kind: in.Kind, Hop: hop,
		Status: entity.MessageQueued, CreatedAt: time.Now(),
	}
	if err := s.Repo.EnqueueMessage(ctx, m); err != nil {
		return nil, err
	}
	if s.Waker != nil {
		if err := s.Waker.WakeChild(ctx, target.ChildSessionID, target.ChildAgent); err != nil {
			return nil, fmt.Errorf("wake @%s: %w", in.ToHandle, err)
		}
	}
	if in.Kind != entity.MessageAsk {
		return &SendResult{MessageID: m.ID, Status: "sent"}, nil
	}
	return s.waitForReply(ctx, m)
}

// waitForReply blocks until the ask is answered or the timeout expires.
func (s *Service) waitForReply(ctx context.Context, ask *entity.AgentMessage) (*SendResult, error) {
	timeout := s.AskTimeout
	if timeout <= 0 {
		timeout = defaultAskTimeout
	}
	deadline := time.After(timeout)
	tick := time.NewTicker(askPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return &SendResult{
				MessageID: ask.ID, Status: "timeout",
				Note: fmt.Sprintf("@%s has not answered yet. The question is still in its inbox — carry on and check back.", ask.ToHandle),
			}, nil
		case <-tick.C:
			reply, err := s.Repo.FindReply(ctx, ask.ID)
			if err != nil {
				return nil, err
			}
			if reply == nil {
				continue
			}
			res := &SendResult{MessageID: ask.ID, Status: "answered", Reply: reply.Body}
			if reply.AutoReply {
				res.Note = "This is @" + reply.FromHandle + "'s closing message for that turn, not a direct reply — it may not answer the question."
			}
			return res, nil
		}
	}
}

// Reply answers an ask. Called by the recipient's explicit reply op, and
// by the turn-end fallback with auto set.
func (s *Service) Reply(ctx context.Context, askID, fromHandle, body string) error {
	return s.reply(ctx, askID, fromHandle, body, false)
}

// AutoReply closes an unanswered ask with the recipient's final text.
//
// Without it, a recipient that simply forgot to call reply would hang the
// sender until the timeout — the commonest failure mode with a model in
// the loop, and the most expensive.
func (s *Service) AutoReply(ctx context.Context, askID, fromHandle, body string) error {
	return s.reply(ctx, askID, fromHandle, body, true)
}

func (s *Service) reply(ctx context.Context, askID, fromHandle, body string, auto bool) error {
	ask, err := s.Repo.GetMessage(ctx, askID)
	if err != nil {
		return err
	}
	if ask == nil {
		return fmt.Errorf("no such question: %s", askID)
	}
	if ask.Status == entity.MessageAnswered {
		return nil // idempotent: an explicit reply that beat the fallback wins
	}
	r := &entity.AgentMessage{
		ID: uuid.NewString(), RootID: ask.RootID,
		FromHandle: fromHandle, ToHandle: ask.FromHandle,
		Body: body, Kind: entity.MessageReply, ReplyTo: askID,
		AutoReply: auto, Hop: ask.Hop,
		Status: entity.MessageDelivered, CreatedAt: time.Now(),
	}
	if err := s.Repo.EnqueueMessage(ctx, r); err != nil {
		return err
	}
	return s.Repo.MarkAnswered(ctx, askID, r.ID)
}

// limits resolves the live ceilings, matching how Run does it.
func (s *Service) limits() Limits {
	if s.LimitsFn != nil {
		return s.LimitsFn()
	}
	return s.Limits
}
```

```go
// internal/agents/delegation/mailbox_repo.go — append

// FindByHandle resolves an address inside one tree. Returns nil when the
// handle does not exist there — addressing never crosses trees, so a
// handle from someone else's conversation simply does not resolve.
func (r *Repo) FindByHandle(ctx context.Context, rootID, handle string) (*entity.AgentDelegation, error) {
	var d entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("root_id = ? AND handle = ?", rootID, handle).First(&d).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// GetMessage loads one message by id, or nil when absent.
func (r *Repo) GetMessage(ctx context.Context, id string) (*entity.AgentMessage, error) {
	var m entity.AgentMessage
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// OldestUnansweredAsk returns the ask a recipient still owes an answer
// to, for the turn-end auto-reply fallback.
func (r *Repo) OldestUnansweredAsk(ctx context.Context, rootID, handle string) (*entity.AgentMessage, error) {
	var m entity.AgentMessage
	err := r.db.WithContext(ctx).
		Where("root_id = ? AND to_handle = ? AND kind = ? AND status = ?",
			rootID, handle, entity.MessageAsk, entity.MessageDelivered).
		Order("created_at asc").First(&m).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}
```

```go
// internal/agents/delegation/run.go — add to the Service struct
	// Waker resumes an exited sub-agent so it can read its inbox.
	// nil = messages queue but nobody is woken.
	Waker Waker
	// AskTimeout bounds a blocking ask. 0 = defaultAskTimeout.
	AskTimeout time.Duration
	// InboxCap bounds undelivered messages per handle. 0 = defaultInboxCap.
	InboxCap int
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agents/delegation/ -run 'TestSend|TestAsk' -count=1`
Expected: PASS

- [ ] **Step 5: Allocate a handle when a delegation is created**

In `Run`, immediately before the `agent_delegations` row is inserted, resolve the tree's taken handles and set `Handle`:

```go
	taken, err := s.Repo.TakenHandles(ctx, rootID)
	if err != nil {
		return nil, fmt.Errorf("resolve handles: %w", err)
	}
	row.Handle = AllocateHandle(req.ProfileKey, taken)
```

- [ ] **Step 6: Full package run**

Run: `go test ./internal/agents/delegation/ -count=1`
Expected: PASS

---

### Task 7: Connector ops — `message`, `reply`, `stop`

**Files:**
- Modify: `internal/connectors/sub-agents/connector.go`, `internal/connectors/sub-agents/handlers.go`, `internal/agents/delegation/acl.go`
- Test: `internal/agents/delegation/acl_test.go`

**Interfaces:**
- Consumes: `(*Service).SendMessage`, `(*Service).Reply` (Task 6), `CanInterrupt` (existing).
- Produces: ops `message`, `reply`, `stop`; `CanAgentStop(d *entity.AgentDelegation, callerRootID string) bool`.

- [ ] **Step 1: Write the failing ACL test**

```go
// internal/agents/delegation/acl_test.go — append
func TestCanAgentStopOnlyInsideItsOwnTree(t *testing.T) {
	d := &entity.AgentDelegation{RootID: "root1", Handle: "worker"}
	if !CanAgentStop(d, "root1") {
		t.Fatal("an agent must be able to stop a peer in its own tree")
	}
	if CanAgentStop(d, "root2") {
		t.Fatal("stopping across trees must be refused")
	}
	if CanAgentStop(nil, "root1") {
		t.Fatal("nil delegation must not be stoppable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestCanAgentStop -count=1`
Expected: FAIL — `undefined: CanAgentStop`

- [ ] **Step 3: Implement the ACL rule**

```go
// internal/agents/delegation/acl.go — append

// CanAgentStop reports whether an agent may stop another agent.
//
// Same tree only. This is the one authority an agent gains over a peer,
// and it is deliberately narrow: takeover.go draws the line at STEERING
// ("letting one agent inject turns into another would blur the delegation
// boundary"), while stopping stays allowed — a leader that can spawn work
// it cannot stop is worse, not safer.
func CanAgentStop(d *entity.AgentDelegation, callerRootID string) bool {
	if d == nil || callerRootID == "" {
		return false
	}
	return d.RootID == callerRootID
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agents/delegation/ -run TestCanAgentStop -count=1`
Expected: PASS

- [ ] **Step 5: Declare the op inputs**

```go
// internal/connectors/sub-agents/connector.go — beside the other op inputs
type messageInput struct {
	To   string `wick:"required;desc=Handle of the agent to message, without the @ (see list_agents)."`
	Body string `wick:"required;textarea;desc=What you want to say or ask."`
	Kind string `wick:"desc=tell (default) sends and returns immediately. ask waits for that agent's answer."`
}

type replyInput struct {
	MessageID string `wick:"required;desc=The id of the question you are answering."`
	Body      string `wick:"required;textarea;desc=Your answer."`
}

type stopInput struct {
	Handle string `wick:"required;desc=Handle of the agent to stop, without the @."`
	Reason string `wick:"desc=Why you are stopping it. Shown to the user and to that agent's record."`
}
```

- [ ] **Step 6: Register the category**

```go
// internal/connectors/sub-agents/connector.go — add to the Operations() slice
		connector.Cat("Messaging", "Talk to the other agents working in this conversation.",
			connector.Op("message", "Message an Agent",
				"Send a message to another agent already working in this conversation, addressed by handle (list_agents shows them). "+
					"kind=tell delivers it and returns immediately — use it to report progress or hand over information. "+
					"kind=ask waits for that agent's answer and returns it, for something you cannot continue without. "+
					"The recipient keeps the context of its own work, so you do not need to re-explain what it is doing. "+
					"Every message counts against this conversation's shared budget and its hop limit: when the hop limit runs out, "+
					"summarise and report to the user instead of messaging again.",
				messageInput{}, h.message, wickdocs.Docs{}),

			connector.Op("reply", "Answer a Question",
				"Answer a question another agent asked you. Pass the message_id from the question. "+
					"If you finish your turn without replying, your closing message is sent as the answer automatically — "+
					"so reply explicitly when the answer matters.",
				replyInput{}, h.reply, wickdocs.Docs{}),

			connector.Op("stop", "Stop an Agent",
				"Stop another agent in this conversation. Its partial work is kept and returned, not discarded. "+
					"Use it when an agent is stuck, redundant, or working on something no longer needed.",
				stopInput{}, h.stop, wickdocs.Docs{}),
		),
```

- [ ] **Step 7: Implement the handlers**

```go
// internal/connectors/sub-agents/handlers.go — append

// messageMaxRunes bounds one message the same way delegate bounds a task.
const messageMaxRunes = 8000

func (h *handlers) message(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	// Sender identity comes from the SESSION, never from input: a model
	// that could name its own sender could impersonate the leader.
	root, from, err := h.deps.treePosition(c.Context(), c.SessionID())
	if err != nil {
		return nil, err
	}
	to := strings.TrimPrefix(strings.TrimSpace(c.Input("to")), "@")
	if to == "" {
		return nil, errors.New("to is required — call list_agents for the handles")
	}
	body := strings.TrimSpace(c.Input("body"))
	if body == "" {
		return nil, errors.New("body is required")
	}
	if len([]rune(body)) > messageMaxRunes {
		return nil, fmt.Errorf("message too long (max %d characters)", messageMaxRunes)
	}
	kind := strings.TrimSpace(c.Input("kind"))
	if kind == "" {
		kind = entity.MessageTell
	}
	if kind != entity.MessageTell && kind != entity.MessageAsk {
		return nil, fmt.Errorf("kind must be tell or ask, got %q", kind)
	}
	_ = caller
	return h.deps.svc().SendMessage(c.Context(), delegation.SendInput{
		RootID: root, FromHandle: from, ToHandle: to, Body: body, Kind: kind,
	})
}

func (h *handlers) reply(c *connector.Ctx) (any, error) {
	if _, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true); err != nil {
		return nil, err
	}
	root, from, err := h.deps.treePosition(c.Context(), c.SessionID())
	if err != nil {
		return nil, err
	}
	_ = root
	id := strings.TrimSpace(c.Input("message_id"))
	if id == "" {
		return nil, errors.New("message_id is required — it is shown with the question")
	}
	body := strings.TrimSpace(c.Input("body"))
	if body == "" {
		return nil, errors.New("body is required")
	}
	if err := h.deps.svc().Reply(c.Context(), id, from, body); err != nil {
		return nil, err
	}
	return map[string]any{"status": "sent"}, nil
}

func (h *handlers) stop(c *connector.Ctx) (any, error) {
	caller, err := h.deps.resolveCaller(c.Context(), c.SessionID(), true)
	if err != nil {
		return nil, err
	}
	root, _, err := h.deps.treePosition(c.Context(), c.SessionID())
	if err != nil {
		return nil, err
	}
	handle := strings.TrimPrefix(strings.TrimSpace(c.Input("handle")), "@")
	target, err := h.deps.svc().Repo.FindByHandle(c.Context(), root, handle)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("no such agent here: @%s", handle)
	}
	if !delegation.CanAgentStop(target, root) {
		return nil, fmt.Errorf("@%s is not part of this conversation", handle)
	}
	out, err := h.deps.svc().Interrupt(c.Context(), target.ID, caller.user.ID, caller.user.IsAdmin())
	if err != nil {
		return nil, err
	}
	return map[string]any{"handle": handle, "outcome": out}, nil
}
```

Add `treePosition` to `Deps` in the same package, resolving the caller's session to `(rootID, handle)`: for a child session, read its `agent_delegations` row; for a leader session, the root is the tree it owns and the handle is `entity.LeaderHandle`.

- [ ] **Step 8: Build and run the package tests**

Run: `go build ./... && go test ./internal/connectors/... ./internal/agents/delegation/ -count=1`
Expected: PASS

---

### Task 8: Deliver inbound messages at the turn boundary

**Files:**
- Modify: `internal/agents/delegation/run.go` (the `onEvent` hook), `internal/tools/agents/subagents.go`, `internal/tools/agents/api_conversation.go`

**Interfaces:**
- Consumes: `DrainInbox`, `FormatInbound`, `OldestUnansweredAsk`, `AutoReply`, `Steerer.SendToChild`, `ResetHops`.
- Produces: `(*Service).DeliverInbox(ctx, rootID, handle string) error`; hop reset on human turns.

- [ ] **Step 1: Write the failing test**

```go
// internal/agents/delegation/mailbox_test.go — append
type recordingSteerer struct {
	mu   sync.Mutex
	sent []string
}

func (r *recordingSteerer) SendToChild(_ context.Context, _, _, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, message)
	return nil
}

func TestDeliverInboxSendsOneBatchedTurn(t *testing.T) {
	r := newTestRepo(t)
	seedTree(t, r)
	st := &recordingSteerer{}
	svc := &Service{Repo: r, Limits: Limits{MaxHops: 100}, Steerer: st, Waker: &fakeWaker{}}
	ctx := context.Background()

	for _, b := range []string{"first", "second"} {
		if _, err := svc.SendMessage(ctx, SendInput{
			RootID: "root1", FromHandle: "main", ToHandle: "reviewer",
			Body: b, Kind: entity.MessageTell,
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	if err := svc.DeliverInbox(ctx, "root1", "reviewer"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(st.sent) != 1 {
		t.Fatalf("want ONE batched turn, got %d", len(st.sent))
	}
	if !strings.Contains(st.sent[0], "first") || !strings.Contains(st.sent[0], "second") {
		t.Fatalf("batch lost a message: %q", st.sent[0])
	}
}

func TestDeliverInboxOnEmptyInboxSendsNothing(t *testing.T) {
	r := newTestRepo(t)
	seedTree(t, r)
	st := &recordingSteerer{}
	svc := &Service{Repo: r, Limits: Limits{MaxHops: 100}, Steerer: st}
	if err := svc.DeliverInbox(context.Background(), "root1", "reviewer"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if len(st.sent) != 0 {
		t.Fatalf("woke an agent for nothing: %v", st.sent)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestDeliverInbox -count=1`
Expected: FAIL — `svc.DeliverInbox undefined`

- [ ] **Step 3: Implement delivery**

```go
// internal/agents/delegation/mailbox.go — append

// inboxBatchMax bounds one delivery. Each turn is a full model round, so
// five messages must not become five turns; the remainder waits for the
// next boundary.
const inboxBatchMax = 10

// DeliverInbox hands a handle's queued messages to it as ONE turn.
//
// Called at a turn boundary, never mid-turn: writing into a child that is
// mid-thought means two writers on one stdin.
func (s *Service) DeliverInbox(ctx context.Context, rootID, handle string) error {
	msgs, err := s.Repo.DrainInbox(ctx, rootID, handle, inboxBatchMax)
	if err != nil || len(msgs) == 0 {
		return err
	}
	target, err := s.Repo.FindByHandle(ctx, rootID, handle)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("%w: %s", ErrUnknownHandle, handle)
	}
	roster, budget, err := s.rosterAndBudget(ctx, rootID)
	if err != nil {
		return err
	}
	if s.Steerer == nil {
		return errors.New("no transport for agent messages")
	}
	return s.Steerer.SendToChild(ctx, target.ChildSessionID, target.ChildAgent,
		FormatInbound(msgs, roster, budget))
}

// rosterAndBudget builds the live address list and remaining allowances
// that ride along with every delivery.
func (s *Service) rosterAndBudget(ctx context.Context, rootID string) ([]RosterEntry, BudgetLine, error) {
	rows, err := s.Repo.ListByRoot(ctx, rootID)
	if err != nil {
		return nil, BudgetLine{}, err
	}
	lim := s.limits()
	b := BudgetLine{TurnsMax: lim.RootBudget, TokensMax: lim.RootTokenBudget, HopMax: lim.MaxHops}
	roster := make([]RosterEntry, 0, len(rows))
	for _, d := range rows {
		if d.Handle == "" {
			continue
		}
		state := "idle"
		switch {
		case d.Status == entity.DelegationRunning:
			state = "working"
		case entity.IsTerminalDelegationStatus(d.Status):
			state = "done"
		}
		roster = append(roster, RosterEntry{Handle: d.Handle, Role: d.ProfileKey, State: state})
		b.TurnsUsed += d.TurnsUsed
		b.TokensUsed += d.TokensUsed
		if d.ID == rootID {
			b.Hop = d.HopCount
		}
	}
	return roster, b, nil
}

// CloseUnansweredAsk promotes a recipient's final text to a reply when it
// finished its turn without answering. Call at turn end, after delivery.
func (s *Service) CloseUnansweredAsk(ctx context.Context, rootID, handle, finalText string) error {
	ask, err := s.Repo.OldestUnansweredAsk(ctx, rootID, handle)
	if err != nil || ask == nil {
		return err
	}
	if strings.TrimSpace(finalText) == "" {
		return nil
	}
	return s.AutoReply(ctx, ask.ID, handle, finalText)
}
```

Add `ListByRoot` to `mailbox_repo.go` if the repo does not already expose it:

```go
// ListByRoot returns every delegation in a tree, for roster and budget.
func (r *Repo) ListByRoot(ctx context.Context, rootID string) ([]entity.AgentDelegation, error) {
	var out []entity.AgentDelegation
	err := r.db.WithContext(ctx).Where("root_id = ?", rootID).
		Order("started_at asc").Find(&out).Error
	return out, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agents/delegation/ -run TestDeliverInbox -count=1`
Expected: PASS

- [ ] **Step 5: Call delivery at the turn boundary**

In the `onEvent` hook in `run.go` that already counts `event.Done` for the turn budget, after the counter update and before the max-turns check, call:

```go
			// A finished turn is the only safe moment to hand a child new
			// input: the process is between reads, so there is no second
			// writer on its stdin.
			if err := s.CloseUnansweredAsk(ctx, rootID, row.Handle, s.Runner.PartialText(childSessionID, agentName)); err != nil {
				l.Warn().Err(err).Str("handle", row.Handle).Msg("mailbox: auto-reply failed")
			}
			if err := s.DeliverInbox(ctx, rootID, row.Handle); err != nil {
				l.Warn().Err(err).Str("handle", row.Handle).Msg("mailbox: inbox delivery failed")
			}
```

- [ ] **Step 6: Reset hops on a human turn**

In the handler that posts a user message into a session (`internal/tools/agents/api_conversation.go`), after the message is accepted:

```go
	// A human speaking is what resets the agent-to-agent hop budget —
	// deliberately not something an agent can do for itself.
	if root := rootForSession(sessionID); root != "" {
		if err := globalDelegation.Repo.ResetHops(c.Context(), root); err != nil {
			l.Warn().Err(err).Str("root", root).Msg("mailbox: hop reset failed")
		}
	}
```

- [ ] **Step 7: Full build + test**

Run: `go build ./... && go test ./internal/agents/... ./internal/tools/agents/ -count=1`
Expected: PASS

---

### Task 9: Rail panel conversation view

**Files:**
- Create: `fe/agents/conversation/src/lib/components/MessageThread.svelte`
- Modify: `fe/agents/conversation/src/lib/components/SubAgentPanel.svelte`, `fe/agents/conversation/src/lib/api/subagents.ts`, `internal/tools/agents/subagents.go`

**Interfaces:**
- Consumes: `GET /api/sessions/{id}/messages`, `POST /api/sessions/{id}/hops/reset`.
- Produces: `AgentMessageItem` TS type; `getMessages`, `bumpHops` client functions; `MessageThread` component.

- [ ] **Step 1: Add the endpoints**

```go
// internal/tools/agents/subagents.go — append, and register in handler.go beside
// the existing /api/sessions/{id}/subagents route.

// sessionMessages returns the agent-to-agent thread for a session's tree.
func sessionMessages(c *tool.Ctx) {
	if globalDelegation == nil {
		c.JSON(map[string]any{"messages": []any{}})
		return
	}
	root := rootForSession(c.Param("id"))
	if root == "" {
		c.JSON(map[string]any{"messages": []any{}})
		return
	}
	msgs, err := globalDelegation.Repo.ListThread(c.Context(), root, 200)
	if err != nil {
		c.Error(500, err.Error())
		return
	}
	c.JSON(map[string]any{"messages": msgs})
}

// resetSessionHops gives a stalled conversation more hops. Human-only by
// construction: it is reachable from the UI, never from an agent op.
func resetSessionHops(c *tool.Ctx) {
	root := rootForSession(c.Param("id"))
	if root == "" || globalDelegation == nil {
		c.Error(404, "no delegation tree for this session")
		return
	}
	if err := globalDelegation.Repo.ResetHops(c.Context(), root); err != nil {
		c.Error(500, err.Error())
		return
	}
	c.JSON(map[string]any{"ok": true})
}
```

- [ ] **Step 2: Write the failing component test**

```ts
// fe/agents/conversation/src/lib/components/MessageThread.test.ts
import { render, screen } from "@testing-library/svelte";
import { describe, it, expect } from "vitest";
import MessageThread from "./MessageThread.svelte";

const msgs = [
  { id: "1", from_handle: "main", to_handle: "reviewer", body: "start please", kind: "tell", created_at: "" },
  { id: "2", from_handle: "reviewer", to_handle: "main", body: "on it", kind: "reply", created_at: "" },
];

describe("MessageThread", () => {
  it("shows who spoke to whom", () => {
    render(MessageThread, { messages: msgs, onBumpHops: () => {}, hopsLeft: 4 });
    expect(screen.getByText("start please")).toBeTruthy();
    expect(screen.getAllByText(/@main/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/@reviewer/).length).toBeGreaterThan(0);
  });

  it("offers more hops only when they have run out", () => {
    const { unmount } = render(MessageThread, { messages: msgs, onBumpHops: () => {}, hopsLeft: 3 });
    expect(screen.queryByRole("button", { name: /hops/i })).toBeNull();
    unmount();
    render(MessageThread, { messages: msgs, onBumpHops: () => {}, hopsLeft: 0 });
    expect(screen.getByRole("button", { name: /hops/i })).toBeTruthy();
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd fe/agents/conversation && npx vitest run src/lib/components/MessageThread.test.ts`
Expected: FAIL — cannot resolve `./MessageThread.svelte`

- [ ] **Step 4: Write the component**

```svelte
<!-- fe/agents/conversation/src/lib/components/MessageThread.svelte -->
<script lang="ts">
  import type { AgentMessageItem } from "../types/agents.js";

  type Props = {
    messages: AgentMessageItem[];
    hopsLeft: number;
    onBumpHops: () => void;
  };

  let { messages, hopsLeft, onBumpHops }: Props = $props();

  // An ask is the only kind that leaves someone waiting, so it is the
  // only kind worth calling out in the list.
  const kindCls = (kind: string) =>
    kind === "ask"
      ? "bg-prog-100 text-prog-400"
      : kind === "reply"
        ? "bg-pos-100 text-pos-400"
        : "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600";
</script>

<div class="flex flex-col gap-2 p-3">
  {#if hopsLeft <= 0}
    <div
      class="flex items-center justify-between gap-2 rounded-lg bg-cau-100 px-3 py-2 text-[11px] text-cau-400"
    >
      <span>Agents have hit their message limit for this turn.</span>
      <button
        type="button"
        onclick={onBumpHops}
        class="shrink-0 rounded px-2 py-1 font-medium bg-cau-200 hover:bg-cau-300 transition-colors"
      >Allow 10 more hops</button>
    </div>
  {/if}

  {#each messages as m (m.id)}
    <div class="rounded-lg border border-white-300 dark:border-navy-600 p-2.5">
      <div class="mb-1 flex items-center gap-1.5 text-[11px]">
        <span class="font-medium text-black-900 dark:text-white-100">@{m.from_handle}</span>
        <span class="text-black-700 dark:text-black-600">→</span>
        <span class="font-medium text-black-900 dark:text-white-100">@{m.to_handle}</span>
        <span class="ml-auto rounded-full px-1.5 py-0.5 {kindCls(m.kind)}">{m.kind}</span>
      </div>
      <p class="whitespace-pre-wrap text-xs text-black-800 dark:text-black-600">{m.body}</p>
      {#if m.auto_reply}
        <p class="mt-1 text-[10px] italic text-black-700 dark:text-black-600">
          Closing message, promoted to a reply automatically.
        </p>
      {/if}
    </div>
  {/each}

  {#if messages.length === 0}
    <p class="px-1 py-6 text-center text-xs text-black-700 dark:text-black-600">
      No messages between agents yet.
    </p>
  {/if}
</div>
```

```ts
// fe/agents/conversation/src/lib/types/agents.ts — append
export type AgentMessageItem = {
  id: string;
  from_handle: string;
  to_handle: string;
  body: string;
  kind: "ask" | "tell" | "reply";
  auto_reply?: boolean;
  created_at: string;
};
```

```ts
// fe/agents/conversation/src/lib/api/subagents.ts — append
export const getMessages = (base: string, id: string) =>
  apiGetE<{ messages: AgentMessageItem[] }>(
    `${base}/api/sessions/${encodeURIComponent(id)}/messages`,
  ).pipe(Effect.map((r) => r?.messages ?? []));

export const bumpHops = (base: string, id: string) =>
  apiPostE<{ ok: boolean }>(
    `${base}/api/sessions/${encodeURIComponent(id)}/hops/reset`,
    {},
  );
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd fe/agents/conversation && npx vitest run src/lib/components/MessageThread.test.ts`
Expected: PASS

- [ ] **Step 6: Mount it in the panel**

Render `MessageThread` under the existing handle list in `SubAgentPanel.svelte`, passing `messages`, `hopsLeft`, and `onBumpHops` down from `DetailView.svelte` (fetch alongside the existing `getSubAgents` call, refetch on the same coalesced trigger).

- [ ] **Step 7: Verify the FE baseline is unchanged**

Run: `cd fe && npx svelte-check --threshold error; npx vitest run`
Expected: error count and failures identical to the pre-change baseline (21 svelte-check errors, 2 vitest failures in `browser.test.ts` at time of writing) — no NEW failures.

---

### Task 10: System prompt + user docs

**Files:**
- Modify: `internal/agents/system-prompt/immutable.md`, `docs/guide/agents/sub-agents.md`

- [ ] **Step 1: Add the mention section to the immutable prompt**

Insert after the existing "Sub-agents (`sub-agents` connector)" block:

```markdown
Agents already working in this conversation have handles (`list_agents`
shows them). `message` reaches one: `kind=tell` delivers and returns,
`kind=ask` waits for that agent's answer. They keep the context of their
own work, so do not re-explain it.

- Message an agent when it knows something you do not, or when your work
  changes what it should be doing. Delegate instead when the work is
  self-contained.
- Every message carries the remaining turns, tokens and hops. When hops
  run out, stop messaging, summarise, and report to the user.
- Answer a question with `reply`. Finishing your turn without replying
  sends your closing message as the answer, which is rarely the answer
  the asker wanted.
```

- [ ] **Step 2: Verify the prompt still renders**

Run: `go test ./internal/agents/system-prompt/ -count=1`
Expected: PASS

- [ ] **Step 3: Write the user-facing guide section**

Add a "Talking to other agents" section to `docs/guide/agents/sub-agents.md` covering: handles and how they are allocated, `tell` vs `ask`, the hop limit and the "Allow 10 more hops" control, and the fact that stopping an agent keeps its partial work. Use `abc.com` / `example.com` in any sample.

- [ ] **Step 4: Full verification**

Run: `go build ./... && go vet ./... && go test ./internal/... -count=1`
Expected: PASS, except the pre-existing `provider/codex` integration failures (2 tests, need a codex binary).

---

### Task 11: A finished sub-agent announces itself as an agent, not as the user

**Files:**
- Modify: `internal/pkg/api/delegation_wiring.go`, `internal/agents/delegation/run.go` (`formatDelivery`), `fe/agents/conversation/src/lib/components/ThreadMessage.svelte`

**Problem this fixes:** `poolDeliverer.DeliverToSession` currently posts an async
result with `session.OriginUI` and role `"user"`
(`delegation_wiring.go:54`). In the transcript that is indistinguishable from
the human typing the result themselves — the leader is told its own operator
said something the operator never said, and the reader cannot see that a
sub-agent came back at all. The whole point of watching agents help each other
is knowing WHICH one spoke and how long it took.

**Interfaces:**
- Consumes: `entity.AgentDelegation.Handle` (Task 1), `StartedAt`/`EndedAt` (existing).
- Produces: source constant `"subagent"`; delivery header `@handle finished · 28m 15s`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agents/delegation/phases_test.go — append
func TestAsyncDeliveryNamesTheAgentAndItsElapsedTime(t *testing.T) {
	row := &entity.AgentDelegation{
		Handle: "debugger", ProfileKey: "socket-debugger",
		StartedAt: time.Now().Add(-28*time.Minute - 15*time.Second),
	}
	end := time.Now()
	row.EndedAt = &end

	text := formatDelivery(row, &Result{Status: entity.DelegationDone, Result: "found it: TCC permission"})

	if !strings.Contains(text, "@debugger") {
		t.Fatalf("delivery does not say which agent answered:\n%s", text)
	}
	if !strings.Contains(text, "28m") {
		t.Fatalf("delivery does not say how long it took:\n%s", text)
	}
	if !strings.Contains(text, "found it: TCC permission") {
		t.Fatalf("delivery lost the result:\n%s", text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agents/delegation/ -run TestAsyncDeliveryNames -count=1`
Expected: FAIL — no handle or elapsed time in the output

- [ ] **Step 3: Put the agent's name and elapsed time in the header**

```go
// internal/agents/delegation/run.go — in formatDelivery, replace the header line
	name := row.Handle
	if name == "" {
		name = row.ProfileKey
	}
	elapsed := ""
	if row.EndedAt != nil && !row.StartedAt.IsZero() {
		elapsed = " · " + row.EndedAt.Sub(row.StartedAt).Round(time.Second).String()
	}
	fmt.Fprintf(&sb, "@%s finished%s\n\n", name, elapsed)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agents/delegation/ -run TestAsyncDeliveryNames -count=1`
Expected: PASS

- [ ] **Step 5: Stop the delivery pretending to be the user**

```go
// internal/pkg/api/delegation_wiring.go:54 — replace OriginUI
	// Source "subagent", not OriginUI: this text did not come from the
	// person at the keyboard, and labelling it as though it did makes the
	// leader answer to instructions its operator never gave.
	return d.pool.Send(ctx, parentSessionID, agentName, "subagent", "user", text)
```

- [ ] **Step 6: Render it as an agent completion, not a channel badge**

```svelte
<!-- fe/agents/conversation/src/lib/components/ThreadMessage.svelte — in sourceBadge -->
    if (src === "subagent") return { label: "Sub-agent", icon: "channel" };
```

- [ ] **Step 7: Verify**

Run: `go test ./internal/agents/... ./internal/pkg/api/ -count=1` and `cd fe && npx vitest run`
Expected: PASS (FE failures identical to baseline)

---

## Self-Review

**Spec coverage**

| Spec section | Task |
|---|---|
| §2.1 Handle | 1 |
| §2.2 Roster + budget footer | 4, 8 (`rosterAndBudget`) |
| §2.3 Server-written sender | 7 (`treePosition`) |
| §3 ask / tell | 6, 7 |
| §5.1 Tables | 1, 2 |
| §5.2 Batch drain, turn boundary, terminal target, inbox cap | 6, 8 |
| §5.2 Resume on idle | 6 (`Waker`) |
| §5.3 ask timeout + auto-reply | 6, 8 (`CloseUnansweredAsk`) |
| §5.4 `@` detection | 5 |
| §6.1 Hop cap + human-only reset | 3, 8 |
| §6.2 Budget reuse | 4, 8 |
| §6.3 Stop by leader | 7 |
| §8 UI | 9 |
| §9 Test matrix | 1–9 |
| §10 Config keys | 3 |

**Gap found and closed:** §5.2's "resume failed → label the message *instance
lost its earlier context*" had no task. It belongs to whoever implements
`Waker` in the pool wiring, which is outside this package. Task 6 Step 3 defines
the interface; the wiring implementation must return a sentinel
(`ErrContextLost`) that `DeliverInbox` turns into a prefixed note. Add this to
Task 6 as Step 7 when implementing:

```go
// ErrContextLost means the child was respawned without its previous
// transcript. The recipient must be told, or it will answer as though it
// remembers a conversation it has actually lost.
var ErrContextLost = errors.New("previous context unavailable")
```

**Placeholder scan:** none — every step carries runnable code or an exact command.

**Type consistency:** `SendInput`/`SendResult`/`RosterEntry`/`BudgetLine`/`Mention`
are defined once and used with the same field names throughout. `entity.LeaderHandle`
is defined in Task 1 and consumed in Tasks 5–8. `Waker` is defined in Task 6 and
referenced in Task 6 only.

---

## Execution record (2026-08-02, branch `feat/agent-mention`, not committed)

All 11 tasks implemented. Three places the plan was wrong and the code
followed reality instead:

1. **`DeliverInbox` returns a count, and a delegation that receives a
   message keeps running.** The plan assumed a turn-boundary hook was
   enough. `run.go` actually ends a delegation on its first turn that
   produces text ("one delegation is one question"), so a message arriving
   mid-work would have been delivered into an agent that was about to
   close. Delivery now reports how many messages landed, and a non-zero
   count continues the run instead of finishing it. This is what makes a
   back-and-forth possible at all; it is bounded by the same turn cap.
2. **`Waker` does not spawn.** `pool.Send` already spawns on demand, so
   waking would have been a second spawn path. `poolWaker` only answers
   the honesty question — will the imminent spawn resume the child's
   transcript, or start a blank one — and returns `ErrContextLost` for the
   latter so the sender is told.
3. **Test helpers already existed.** `testRepo` (shared-cache sqlite) and
   `recordingSteerer` were in `governor_test.go` / `phases_test.go`; the
   plan's duplicates were dropped rather than added alongside.

`stop` is registered as `Op`, not `OpDestructive`: stopping returns partial
work as a normal result (interrupt.go's existing doctrine), and a
destructive op defaults off on every row, which would leave a leader able
to start work it cannot stop.

### Verification

- `go build ./internal/... ./cmd/... ./pkg/...` + `go vet ./internal/...` — clean.
  (`go build ./...` fails in `template/`, which ships `go.mod.tmpl` and is not a
  buildable module — pre-existing.)
- Touched packages pass: `delegation`, `connectors/sub-agents`, `pkg/api`,
  `pkg/postgres`, `entity`, `system-prompt`, `tools/agents`.
- Full `./internal/...`: one failure, `TestScenario_ConcurrentSessionsQueueDrains`
  in `internal/agents`. Reproduced on a stashed clean tree — **pre-existing and
  flaky**, not caused by this work. `internal/agents/gate` failed once under
  parallel load and passed on its own.
- FE `npm run test`: only `browser.test.ts` (2 tests) fails — the documented
  baseline. `agents-scm` reports "no test files", also baseline.
- `svelte-check`: 59 errors, **none** in `MessageThread.svelte`,
  `SubAgentPanel.svelte`, or the lines touched in `DetailView.svelte`.

---

## Follow-up landed the same session

Two things the original plan did not cover, both requested after it was
written.

### 1. `@` in the composer offers agents, not only files

The composer's `@` was a file picker. It now lists **agents first, files
after** — live handles in this tree (`@reviewer · running here`) then
roles in scope (`@researcher · <description>`). Agents rank above files
because naming an agent asks for work while naming a file only supplies
context.

`Composer.svelte` gained an optional `mentionAgents` prop, so every other
consumer of the shared composer is untouched. `SubAgentItem` now carries
`handle` so the picker can address a running instance rather than its
role.

Picking a name only inserts text — wick does not intercept and auto-route
it. The immutable prompt carries the rule instead: *when the USER writes
`@name`, use that agent — message the handle if it is running, otherwise
delegate to the role of that name, and say so if the name resolves to
nothing.* Hard-routing was rejected: it would fire on a name written
mid-sentence and take the decision away from the leader, which is the
one participant that can see what the user actually meant.

### 2. An agent can manage roles over MCP

`create_agent` is now create-or-**patch** (omitted fields keep their
value, so raising a turn budget cannot silently drop a prompt) and
accepts `allowed_tags`, `can_delegate`, `allow_take_over`, `mode`.

New op `list_access` returns the tags the caller can grant, as
`{id, name}`. Without it the model could only guess ids, and a guessed id
is silently dropped by the narrowing rule — indistinguishable from a role
that legitimately has no tools.

`delegation.NarrowTags` intersects a requested tag set with the caller's
own, so an agent choosing a role's tool access can only ever pick a
SUBSET of what the triggering human already holds. That is the whole
reason this is safe to expose to a model.

**`can_delegate` is now enforced**, not just stored. `Deps.callerMayDelegate`
refuses `delegate` and `create_agent` from a sub-agent whose role did not
opt in. Exposing the field while nothing read it would have been a knob
that lies. It fails OPEN on an unresolvable role rather than stranding a
running tree — depth and budget caps still bound that case.

**Deliberately NOT exposed to agents:**

- The governor (`sub_agents_enabled`, depth, budget, parallel, hops). A
  ceiling an agent can raise is not a ceiling — the same argument that
  keeps the hop reset human-only.
- `strict_mcp` and `allowed_native_tools`. Both are still dead knobs
  (`WICK_STRICT_MCP` decides MCP isolation globally). Letting a model set
  them would add two more settings that do nothing, which this design
  explicitly refuses to do.
