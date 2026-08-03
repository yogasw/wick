package delegation

import (
	"encoding/json"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// Structured results.
//
// A sub-agent's answer used to be a block of prose. A supervisor that has
// to act on it — merge evidence, decide whether to ask for more, gate a
// customer reply on confidence — then had to parse that prose with
// another model call. This is the typed version of the same answer.
//
// The prose does not go away. Result stays authoritative for humans and
// for every existing caller; the envelope is additive.

// Confidence levels. An enum, not a number: a decimal from a language
// model is false precision, and every decision made on it is a threshold
// comparison that three buckets answer just as well.
const (
	ConfidenceUnknown = "unknown"
	ConfidenceLow     = "low"
	ConfidenceMedium  = "medium"
	ConfidenceHigh    = "high"
)

// Storage bounds on reported evidence. Without them a sub-agent can turn
// its token cap into a database problem by quoting a whole log file.
const (
	maxEvidenceItems = 20
	maxExcerptBytes  = 4096
)

// Evidence kinds.
const (
	EvidenceLog         = "log"
	EvidenceCode        = "code"
	EvidenceDoc         = "doc"
	EvidenceData        = "data"
	EvidenceObservation = "observation"
)

// Evidence is one verifiable excerpt a sub-agent is prepared to stand
// behind. Source and excerpt are both required by the intake gate: a
// claim nobody else can check is a guess wearing a finding's clothes.
type Evidence struct {
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Excerpt string `json:"excerpt"`
}

// NextTask is work a sub-agent recommends someone else pick up.
type NextTask struct {
	Role   string `json:"role"`
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

// CheckerDecision is the evidence-checker role's verdict. Declared here
// so the envelope is one type; the behaviour that fills it lives with the
// checker loop.
type CheckerDecision struct {
	Decision          string     `json:"decision"`
	ConfirmedFindings []string   `json:"confirmed_findings,omitempty"`
	Contradictions    []string   `json:"contradictions,omitempty"`
	MissingEvidence   []string   `json:"missing_evidence,omitempty"`
	FollowupTasks     []NextTask `json:"followup_tasks,omitempty"`
}

// ResultEnvelope is a sub-agent's answer in a shape a supervisor can act
// on without re-reading prose.
type ResultEnvelope struct {
	Summary              string     `json:"summary"`
	Findings             []string   `json:"findings,omitempty"`
	Evidence             []Evidence `json:"evidence,omitempty"`
	Confidence           string     `json:"confidence"`
	NeedsFollowup        bool       `json:"needs_followup,omitempty"`
	RecommendedNextTasks []NextTask `json:"recommended_next_tasks,omitempty"`
	// Structured is false when this envelope was reconstructed from the
	// final text because report_result was never called. Visible rather
	// than silent: a supervisor reading it knows the fields it wants were
	// never actually asserted.
	Structured bool `json:"structured"`
	// Checker is filled only by the evidence-checker role.
	Checker *CheckerDecision `json:"checker,omitempty"`
}

// Clamp normalises an envelope to what may be stored.
//
// Excerpts are cut by BYTES, not runes: the cap is a storage bound, and a
// truncated excerpt is already a fragment — spending a rune scan to make
// the fragment end tidily buys nothing.
func (e *ResultEnvelope) Clamp() {
	if e == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(e.Confidence)) {
	case ConfidenceLow:
		e.Confidence = ConfidenceLow
	case ConfidenceMedium:
		e.Confidence = ConfidenceMedium
	case ConfidenceHigh:
		e.Confidence = ConfidenceHigh
	default:
		// Anything else — a decimal, a sentence, an empty string — is not
		// a confidence. Saying "unknown" is honest; guessing is not.
		e.Confidence = ConfidenceUnknown
	}
	if len(e.Evidence) > maxEvidenceItems {
		e.Evidence = e.Evidence[:maxEvidenceItems]
	}
	for i := range e.Evidence {
		if len(e.Evidence[i].Excerpt) > maxExcerptBytes {
			e.Evidence[i].Excerpt = e.Evidence[i].Excerpt[:maxExcerptBytes]
		}
	}
}

// FallbackEnvelope reconstructs an envelope from prose, for a sub-agent
// that finished without calling report_result.
//
// Failing the run instead would punish the LEADER for the sub-agent's
// omission and throw away work that has already been paid for.
func FallbackEnvelope(finalText string) *ResultEnvelope {
	return &ResultEnvelope{
		Summary:    strings.TrimSpace(finalText),
		Confidence: ConfidenceUnknown,
		Structured: false,
	}
}

// EnvelopeOf reads the stored envelope, or nil when there is none.
//
// A malformed column reads as "no envelope" rather than an error: the
// envelope is an enrichment, and a supervisor is better served by prose
// with no structure than by a failed delegation.
func EnvelopeOf(d *entity.AgentDelegation) *ResultEnvelope {
	if d == nil || strings.TrimSpace(d.ResultJSON) == "" {
		return nil
	}
	var e ResultEnvelope
	if err := json.Unmarshal([]byte(d.ResultJSON), &e); err != nil {
		return nil
	}
	return &e
}
