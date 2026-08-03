package delegation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// The incident store.
//
// Somewhere to put what an investigation has established, so "what do we
// know so far?" is a query rather than a leader's recollection. One
// incident per delegation tree, created the moment there is something to
// record and not before.

// ErrIncidentClosed means the incident is terminal and refuses writes.
var ErrIncidentClosed = errors.New("incident is closed")

// EnsureForRoot returns the tree's incident, creating it on first call.
//
// Idempotent under concurrency: the unique index on root_id decides the
// winner and the loser re-reads. Doing it that way rather than with a
// check-then-insert matters because two investigators finishing together
// is the normal case, not the rare one.
func (s *Service) EnsureForRoot(ctx context.Context, rootID string) (*entity.AgentIncident, error) {
	if rootID == "" {
		return nil, errors.New("incident: no delegation tree for this conversation")
	}
	if inc, err := s.IncidentForRoot(ctx, rootID); err != nil || inc != nil {
		return inc, err
	}

	inc := &entity.AgentIncident{
		ID:     uuid.NewString(),
		RootID: rootID,
		Status: entity.IncidentInvestigating,
	}
	// Scope follows the tree: an incident that did not know its project
	// would be readable from a session that has no business with it.
	if root, err := s.Repo.Get(ctx, rootID); err == nil && root != nil {
		inc.ProjectID = root.ProjectID
		inc.TriggeredBy = root.TriggeredBy
	}

	err := s.Repo.DB().WithContext(ctx).Create(inc).Error
	if err == nil {
		return inc, nil
	}
	// Somebody else won the race. Their row is as good as ours.
	if existing, rerr := s.IncidentForRoot(ctx, rootID); rerr == nil && existing != nil {
		return existing, nil
	}
	return nil, err
}

// IncidentForRoot returns the tree's incident, or (nil, nil) when there
// is none. "Nothing recorded yet" is a normal state, not a lookup
// failure.
func (s *Service) IncidentForRoot(ctx context.Context, rootID string) (*entity.AgentIncident, error) {
	if s == nil || s.Repo == nil || rootID == "" {
		return nil, nil
	}
	var inc entity.AgentIncident
	err := s.Repo.DB().WithContext(ctx).Where("root_id = ?", rootID).First(&inc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inc, nil
}

// evidenceFingerprint identifies a piece of evidence by its content, so
// the same log line found twice is one row.
func evidenceFingerprint(e Evidence) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(e.Kind) + "|" +
		strings.TrimSpace(e.Source) + "|" + strings.TrimSpace(e.Excerpt)))
	return hex.EncodeToString(sum[:])
}

// IngestEvidence writes an envelope's evidence to the tree's incident and
// reports how many rows were new.
//
// Only Evidence is ingested. Findings are INTERPRETATIONS, and merging
// four agents' interpretations without a checker is how a supervisor ends
// up confidently wrong; the checker promotes them later, from evidence
// that was actually quoted.
func (s *Service) IngestEvidence(ctx context.Context, row *entity.AgentDelegation, env *ResultEnvelope) (int, error) {
	if row == nil || env == nil || len(env.Evidence) == 0 {
		return 0, nil
	}
	inc, err := s.EnsureForRoot(ctx, row.RootID)
	if err != nil || inc == nil {
		return 0, err
	}

	added := 0
	db := s.Repo.DB().WithContext(ctx)
	for _, e := range env.Evidence {
		rec := entity.AgentEvidence{
			ID:           uuid.NewString(),
			IncidentID:   inc.ID,
			Fingerprint:  evidenceFingerprint(e),
			DelegationID: row.ID,
			Role:         row.ProfileKey,
			Kind:         e.Kind,
			Source:       e.Source,
			Excerpt:      e.Excerpt,
		}
		// A conflict is a SUCCESSFUL no-op: the fact is already recorded
		// and the second finder corroborated rather than erred.
		res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec)
		if res.Error != nil {
			return added, res.Error
		}
		added += int(res.RowsAffected)
	}

	// Stamp the link so a finding can be traced back from either side.
	if row.IncidentID != inc.ID {
		if err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentDelegation{}).
			Where("id = ?", row.ID).Update("incident_id", inc.ID).Error; err != nil {
			log.Warn().Err(err).Str("delegation", row.ID).
				Msg("delegation: incident link not stamped")
		}
		row.IncidentID = inc.ID
	}
	return added, nil
}

// ListEvidence returns an incident's evidence, oldest first.
func (s *Service) ListEvidence(ctx context.Context, incidentID string) ([]entity.AgentEvidence, error) {
	var out []entity.AgentEvidence
	err := s.Repo.DB().WithContext(ctx).
		Where("incident_id = ?", incidentID).
		Order("created_at asc").Find(&out).Error
	return out, err
}

// CountEvidence is the cheap version of ListEvidence, for deciding
// whether a round established anything new.
func (s *Service) CountEvidence(ctx context.Context, incidentID string) (int, error) {
	var n int64
	err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentEvidence{}).
		Where("incident_id = ?", incidentID).Count(&n).Error
	return int(n), err
}

// IncidentPatch is a partial update. A nil field is untouched, which is
// what lets a supervisor add a hypothesis without restating the summary.
type IncidentPatch struct {
	Title           *string
	UserIssue       *string
	Summary         *string
	Status          *string
	StopReason      *string
	Hypotheses      *string
	MissingEvidence *string
	NextActions     *string
	ClientContext   *string
}

// UpdateIncident applies a patch to a tree's incident, creating the
// incident if this is the first thing anyone recorded.
func (s *Service) UpdateIncident(ctx context.Context, rootID string, p IncidentPatch) (*entity.AgentIncident, error) {
	inc, err := s.EnsureForRoot(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if inc.Status == entity.IncidentClosed {
		return nil, ErrIncidentClosed
	}

	fields := map[string]any{}
	setStr := func(col string, v *string) {
		if v != nil {
			fields[col] = *v
		}
	}
	setStr("title", p.Title)
	setStr("user_issue", p.UserIssue)
	setStr("summary", p.Summary)
	setStr("stop_reason", p.StopReason)
	setStr("hypotheses", p.Hypotheses)
	setStr("missing_evidence", p.MissingEvidence)
	setStr("next_actions", p.NextActions)
	setStr("client_context", p.ClientContext)
	if p.Status != nil {
		st := strings.TrimSpace(*p.Status)
		switch st {
		case entity.IncidentInvestigating, entity.IncidentConfirmed, entity.IncidentEscalated:
			fields["status"] = st
		case entity.IncidentClosed:
			// Closing carries a final summary and is its own operation.
			return nil, errors.New("use action=close to close an incident, so it carries a final summary")
		default:
			return nil, errors.New("status must be investigating, confirmed, or escalated")
		}
	}
	if len(fields) == 0 {
		return inc, nil
	}

	if err := s.Repo.DB().WithContext(ctx).Model(&entity.AgentIncident{}).
		Where("id = ?", inc.ID).Updates(fields).Error; err != nil {
		return nil, err
	}
	return s.IncidentForRoot(ctx, rootID)
}

// CloseIncident writes a terminal status and a final summary.
//
// Guarded on the current status so a second close is a refusal rather
// than a silent overwrite of the first one's summary. Reopening is a
// human action in the UI, matching how role locking already works.
func (s *Service) CloseIncident(ctx context.Context, rootID, finalSummary string) (*entity.AgentIncident, error) {
	finalSummary = strings.TrimSpace(finalSummary)
	if finalSummary == "" {
		return nil, errors.New("final_summary is required to close an incident — it is what anyone reading this later gets")
	}
	inc, err := s.IncidentForRoot(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if inc == nil {
		return nil, errors.New("no incident recorded for this conversation")
	}
	res := s.Repo.DB().WithContext(ctx).Model(&entity.AgentIncident{}).
		Where("id = ? AND status <> ?", inc.ID, entity.IncidentClosed).
		Updates(map[string]any{
			"status":        entity.IncidentClosed,
			"final_summary": finalSummary,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrIncidentClosed
	}
	return s.IncidentForRoot(ctx, rootID)
}

// decodeJSONList reads one of the embedded JSON array columns, tolerating
// junk as "no entries". These columns feed prompts, and a malformed one
// should cost a line of context rather than a delegation.
func decodeJSONList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
