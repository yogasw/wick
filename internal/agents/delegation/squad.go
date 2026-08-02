package delegation

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Squads (Phase 5): a named, fixed line-up of roles.
//
// Without one, a leader may delegate to every profile its caller's tags
// allow. That is right for ad-hoc work and vague for a recurring job. A
// squad pins the roster so "the release crew" means the same three roles
// every time.
//
// A squad NARROWS and never widens: membership is intersected with what
// the calling human could already reach, so adding a profile to a squad
// cannot hand anyone new access.

// ErrSquadNotFound is returned when a squad key does not resolve.
var ErrSquadNotFound = errors.New("squad not found")

// ListSquads returns squads, optionally including disabled ones.
func (r *Repo) ListSquads(ctx context.Context, includeDisabled bool) ([]entity.AgentSquad, error) {
	q := r.db.WithContext(ctx).Order("key asc")
	if !includeDisabled {
		q = q.Where("disabled = ?", false)
	}
	var out []entity.AgentSquad
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetSquad resolves one squad by key.
func (r *Repo) GetSquad(ctx context.Context, key string) (*entity.AgentSquad, error) {
	var sq entity.AgentSquad
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&sq).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSquadNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sq, nil
}

// SaveSquad inserts or updates a squad.
func (r *Repo) SaveSquad(ctx context.Context, sq *entity.AgentSquad) error {
	return r.db.WithContext(ctx).Save(sq).Error
}

// DeleteSquad removes a squad by id.
func (r *Repo) DeleteSquad(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&entity.AgentSquad{}).Error
}

// SquadMembers decodes a squad's member profile keys.
func SquadMembers(sq *entity.AgentSquad) []string {
	if sq == nil {
		return nil
	}
	return decodeStringSlice(sq.MemberProfileKeys)
}

// EncodeSquadMembers serialises a member list.
func EncodeSquadMembers(keys []string) string { return encodeStringSlice(keys) }

// SquadAllows reports whether a squad permits delegating to profileKey.
// An empty roster means "no restriction" — a squad that names a leader
// but no members is a label, not a cage.
func SquadAllows(sq *entity.AgentSquad, profileKey string) bool {
	members := SquadMembers(sq)
	if len(members) == 0 {
		return true
	}
	for _, m := range members {
		if m == profileKey {
			return true
		}
	}
	return false
}

// FilterBySquad narrows a visible roster to a squad's members.
//
// Applied AFTER tag filtering, never instead of it: a squad expresses
// intent ("these roles work together"), while tags express permission.
// Reversing the order would let squad membership imply access.
func FilterBySquad(profiles []entity.AgentProfile, sq *entity.AgentSquad) []entity.AgentProfile {
	if sq == nil {
		return profiles
	}
	members := SquadMembers(sq)
	if len(members) == 0 {
		return profiles
	}
	allowed := make(map[string]bool, len(members))
	for _, m := range members {
		allowed[m] = true
	}
	out := make([]entity.AgentProfile, 0, len(profiles))
	for _, p := range profiles {
		if allowed[p.Key] {
			out = append(out, p)
		}
	}
	return out
}
