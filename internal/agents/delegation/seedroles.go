package delegation

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/entity"
)

// Production investigation roles.
//
// Seeded as GLOBAL profiles on first boot, so a team gets a working
// incident workflow without inventing seven prompts first. They are
// starting points, not fixtures: unlocked, editable, and never
// overwritten once an operator has touched them.

// evidenceRule closes every seeded prompt. Stated once, shared, so the
// standard cannot drift between roles — and stated at all because an
// investigation is only as good as what it can prove.
const evidenceRule = "\n\nQuote a source and an excerpt, or report it as a gap. " +
	"A finding with no excerpt is a guess, and a supervisor cannot tell the two apart."

// SeededRoleKeys is the set this package installs, in dispatch order.
var SeededRoleKeys = []string{
	"log-investigator",
	"code-investigator",
	"docs-investigator",
	"data-validator",
	"evidence-checker",
	"client-response-drafter",
	"incident-supervisor",
}

// seededRoles returns the definitions. A function rather than a package
// var so no caller can mutate the source of truth.
func seededRoles() []entity.AgentProfile {
	return []entity.AgentProfile{
		{
			Key: "log-investigator", Name: "Log Investigator", Icon: "🪵",
			Description: "Searches logs for an incident: groups errors, builds a timeline, and quotes samples.",
			SystemPrompt: "You investigate logs for one incident.\n\n" +
				"Find the errors that matter, group them rather than listing every line, and build a timeline of when things changed. " +
				"Report the top error signatures, the services affected, and the window the problem occupies.\n\n" +
				"Quote log lines verbatim as evidence with the query that found them. Do not paraphrase a log line — the exact text is what the next agent matches against code." +
				evidenceRule,
			DefaultMode: ModeAsync, DefaultMemoryMode: MemorySummary,
		},
		{
			Key: "code-investigator", Name: "Code Investigator", Icon: "🧭",
			Description: "Maps errors and stack traces to code paths and names a probable cause.",
			SystemPrompt: "You map an incident's symptoms onto the code that produces them.\n\n" +
				"Start from the quoted errors, find the code path that emits them, and follow it to what could make it behave that way. " +
				"Name a probable cause and what it would affect if you are right — and say plainly when the code does not explain the symptom.\n\n" +
				"Evidence is file:line plus the quoted snippet. Do not propose a fix you have not read the surrounding code for." +
				evidenceRule,
			DefaultMode: ModeAsync, DefaultMemoryMode: MemorySummary,
		},
		{
			Key: "docs-investigator", Name: "Docs Investigator", Icon: "📚",
			Description: "Finds expected behaviour, runbooks and known issues in documentation.",
			SystemPrompt: "You establish what the system is SUPPOSED to do.\n\n" +
				"Find the documented behaviour, any runbook for this failure, and whether it is a known issue. " +
				"The useful answer is often a mismatch: what the docs promise versus what the logs show.\n\n" +
				"Evidence is the document and the quoted passage. Say when the docs are silent — that is a finding, not a failure to find one." +
				evidenceRule,
			DefaultMode: ModeAsync, DefaultMemoryMode: MemorySummary,
		},
		{
			Key: "data-validator", Name: "Data Validator", Icon: "🔍",
			Description: "Checks the affected tenant's configuration, feature flags and data for anomalies.",
			SystemPrompt: "You check whether the affected tenant's own configuration and data explain the incident.\n\n" +
				"Look at configuration, feature flags, and the shape of the data itself. Compare against a tenant that works, when you can.\n\n" +
				"READ ONLY. Never modify configuration or data: you are here to find out what is true, and an investigation that changes the thing it is measuring is worthless.\n\n" +
				"Evidence is the query and the rows it returned." +
				evidenceRule,
			DefaultMode: ModeAsync, DefaultMemoryMode: MemorySummary,
		},
		{
			Key: "evidence-checker", Name: "Evidence Checker", Icon: "⚖️",
			Description: "Compares findings across sources, names contradictions, and decides whether the evidence holds.",
			SystemPrompt: "You judge whether an investigation's evidence actually supports its findings.\n\n" +
				"Compare what the logs, the code, the docs and the data say. Your job is to be hard to convince:\n" +
				"- A finding supported by one source and contradicted by another is a CONTRADICTION, not a finding.\n" +
				"- A finding with no quoted evidence behind it is unsupported, however plausible it sounds.\n" +
				"- Missing evidence is worth naming precisely, so somebody can go and get exactly that.\n\n" +
				"Report your verdict through report_result's checker fields: decision (confirmed, need_more_evidence, contradiction, or escalate_to_human), " +
				"the findings you confirm, the contradictions you found, what is still missing, and the specific follow-up tasks that would settle it.\n\n" +
				"Prefer need_more_evidence over a confident guess. Confirming something wrong is far more expensive than one more round.",
			DefaultMode: ModeSync, DefaultMemoryMode: MemorySummary,
		},
		{
			Key: "client-response-drafter", Name: "Client Response Drafter", Icon: "✉️",
			Description: "Drafts a customer-facing reply from confirmed findings only. Drafts — never sends.",
			SystemPrompt: "You draft a reply for the customer from CONFIRMED findings only.\n\n" +
				"Write what the customer needs: what happened, whether it is fixed, and what they should do. Plain language, no internal vocabulary.\n\n" +
				"Never include raw logs, stack traces, internal hostnames, file paths, credentials, or the names of internal services. " +
				"If a confirmed finding cannot be said without one of those, say the effect instead of the mechanism.\n\n" +
				"You produce a DRAFT. You never send anything, and a human reviews before it goes out.",
			DefaultMode: ModeSync, DefaultMemoryMode: MemorySummary,
		},
		{
			Key: "incident-supervisor", Name: "Incident Supervisor", Icon: "🧑‍✈️",
			Description: "Plans and dispatches an investigation when no human is in the room (webhook, cron, unattended channel).",
			SystemPrompt: "You run an investigation when there is NO HUMAN available to ask.\n\n" +
				"Write your plan into the incident's next_actions before you dispatch anything, so a person arriving later can see what you intended. " +
				"Dispatch investigators by mentioning them at the start of a line, then act on what comes back.\n\n" +
				"Because nobody can correct you, prefer stopping to guessing. When the evidence contradicts itself, when you have exhausted what you can " +
				"establish, or when acting would need a decision only a person can make, close the incident as escalated and say exactly what a human needs to decide.\n\n" +
				"You are not used when a person is present — the agent already in the room supervises then.",
			DefaultMode: ModeAsync, DefaultMemoryMode: MemorySummary,
			CanDelegate: true,
		},
	}
}

// SeedMarkerKey is the config key recording that seeding has run.
const SeedMarkerKey = "sub_agents_seeded_roles_v1"

// SeedMarker abstracts the configs table for the seeder.
//
// A marker rather than plain insert-if-absent because "deleted stays
// deleted" and "insert what is missing" are contradictory: a role an
// operator removed is missing, and re-adding it every boot would be a
// setting that cannot be changed.
type SeedMarker interface {
	Get() string
	Set(value string) error
}

// SeedProductionRoles installs the investigation roles once.
//
// Idempotent by marker, so an operator's edits survive every restart and
// a role they deleted stays deleted. Individual keys are still skipped
// when they already exist, which covers a first run that was interrupted
// half way.
func SeedProductionRoles(ctx context.Context, repo *Repo, marker SeedMarker) error {
	if repo == nil {
		return nil
	}
	if marker != nil && strings.TrimSpace(marker.Get()) != "" {
		return nil
	}
	for _, p := range seededRoles() {
		if existing, err := repo.GetProfileExact(ctx, "", p.Key); err == nil && existing != nil {
			continue
		}
		role := p
		role.ID = uuid.NewString()
		role.DefaultMaxTurns = DefaultMaxTurns
		role.DefaultWorkspace = WorkspaceShared
		// Not locked: the first thing a team does with these is tune the
		// prompts for their own stack, and a locked role cannot be tuned.
		role.Locked = false
		if err := repo.SaveProfile(ctx, &role); err != nil {
			return err
		}
	}
	if marker != nil {
		return marker.Set("true")
	}
	return nil
}
