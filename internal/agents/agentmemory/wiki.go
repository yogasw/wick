package agentmemory

import (
	"context"
	"encoding/json"
)

// The read/act surface behind the Wiki and Handoffs tabs.
//
// Each one is an OPTIONAL interface for the same reason Compactor and
// ProjectBriefer are: a backend that cannot answer "give me this page" should
// say so by not implementing it, and get a 501, rather than return an empty
// page that reads as "the page is blank".

// PageReader reads one wiki page in full. Search only ever returns a snippet,
// so without this the Wiki tab can find a page and never show it.
type PageReader interface {
	// ReadPage fetches the page at the store-relative path
	// ("sessions/<uuid>.md"). The scope must be explicit for the same reason
	// a briefing's must be: the backend resolves an unnamed scope from
	// somewhere else and answers about a different project (PLAN §14).
	ReadPage(ctx context.Context, conn Conn, s ReadScope, path string) (*Page, error)
}

// Page is one wiki page with its body. Every field was read off a real
// `ai-memory 2.4.0 read-page --json` (2026-09-25).
//
// There is no updated_at and no kind here even though SearchHit has one: the
// read-page document simply does not carry them, and inventing them from the
// search hit that led here would put a second source's data under this one's
// name.
type Page struct {
	Path      string `json:"path"`
	Workspace string `json:"workspace,omitempty"`
	Project   string `json:"project,omitempty"`
	Title     string `json:"title,omitempty"`
	// Body is the full markdown. For a session page it already contains the
	// "## Raw observations" section, which is the only surface wick has for
	// a session's raw events: there is no CLI subcommand that lists them,
	// and the MCP tool that does would be a third exception to the
	// no-MCP rule (PLAN §13.2.1) that nobody authorised.
	Body string `json:"body"`
	// Frontmatter is the page's own metadata, carried raw. Its keys differ
	// per page kind (a session page has session_id/agent/tier, a concept
	// page does not), so modelling it would drop whatever wick guessed
	// wrong about.
	Frontmatter json.RawMessage `json:"frontmatter,omitempty"`
}

// MessageLister reads the cross-project mailbox — the other half of "two
// agents working the same store", next to handoffs.
type MessageLister interface {
	// Messages lists one project's pending mail. box is "inbox" (addressed
	// to this project) or "outbox" (sent by it and still cancellable).
	Messages(ctx context.Context, conn Conn, s ReadScope, box string, limit int) ([]Message, error)
}

// Message is one cross-project message.
//
// FromProjectID is a UUID and there is no name for it anywhere — not in the
// CLI listing, not in the MCP one, and the project listing has no ids to join
// against (verified 2026-09-25). So the sender is shown as the id it is. A
// resolved-looking name here would be a guess about which project sent a
// message, which is precisely the thing an operator would act on.
type Message struct {
	ID              string `json:"id"`
	Subject         string `json:"subject,omitempty"`
	Body            string `json:"body,omitempty"`
	FromAgent       string `json:"from_agent,omitempty"`
	FromWorkspaceID string `json:"from_workspace_id,omitempty"`
	FromProjectID   string `json:"from_project_id,omitempty"`
	State           string `json:"state,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	ClaimedAt       string `json:"claimed_at,omitempty"`
}

// HandoffCanceller retires ONE stale handoff by id.
//
// Why this is the feature's second MCP call (PLAN §20.2): the CLI's only
// cancellation is `handoffs --expire-all`, which discards every open handoff
// including the ones another agent is waiting on. Per-id cancellation exists
// only as `memory_handoff_cancel`. So the exception is taken because the MCP
// path is SAFER than the CLI one, not more convenient — and --expire-all is
// never exposed in the UI at all.
type HandoffCanceller interface {
	CancelHandoff(ctx context.Context, conn Conn, s ReadScope, id string) (*HandoffCancelResult, error)
}

// HandoffCancelResult is one cancellation's outcome.
//
// Cancelled false with no error is a real, ordinary answer: the handoff was
// already gone (expired, or accepted by another agent between the listing and
// the click). The UI must say that rather than report a success — the operator
// who reads "cancelled" believes they stopped something that had already
// stopped itself, and stops looking for the agent that took it.
type HandoffCancelResult struct {
	HandoffID string `json:"handoff_id"`
	Cancelled bool   `json:"cancelled"`
	State     string `json:"state,omitempty"`
}

// Sweeper runs the retention sweep — the button behind Settings § retention.
type Sweeper interface {
	// ForgetSweep evicts cold pages and, when a positive retention age is
	// configured, prunes raw observations. DryRun reports what WOULD go.
	ForgetSweep(ctx context.Context, conn Conn, s ReadScope, dryRun bool) (*SweepReport, error)
}

// SweepReport is one sweep's outcome. Output is the backend's own text: like
// compact, `forget-sweep` has no --json mode, so wick carries the sentence
// through instead of inventing a structure the CLI never promised.
type SweepReport struct {
	DryRun bool   `json:"dry_run"`
	Output string `json:"output"`
}
