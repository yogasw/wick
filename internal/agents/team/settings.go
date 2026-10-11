package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// MaxTeamPromptBytes caps the Team instructions. They ride in every turn
// of every agent the owner has, so the cap is about prompt cost, not
// storage.
const MaxTeamPromptBytes = 16 * 1024

// ValidateTeamPrompt reports why s cannot be saved as Team instructions.
func ValidateTeamPrompt(s string) error {
	if len(s) > MaxTeamPromptBytes {
		return fmt.Errorf("team prompt is %d bytes, the limit is %d", len(s), MaxTeamPromptBytes)
	}
	return nil
}

// Settings returns userID's Team settings; a user who never saved any
// gets the zero value (the defaults).
func (s *Store) Settings(ctx context.Context, userID string) (entity.TeamSettings, error) {
	row := entity.TeamSettings{UserID: userID}
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.TeamSettings{UserID: userID}, nil
	}
	return row, err
}

// SaveSettings writes the whole row of st.UserID, creating it on the first
// save.
func (s *Store) SaveSettings(ctx context.Context, st *entity.TeamSettings) error {
	if st.UserID == "" {
		return errors.New("team settings: no user")
	}
	if err := ValidateTeamPrompt(st.Prompt); err != nil {
		return err
	}
	st.UpdatedAt = time.Now().UTC()
	// UpdateAll: a column added to entity.TeamSettings is saved without
	// touching this list.
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		UpdateAll: true,
	}).Create(st).Error
}

// idList joins ids for an error message, naming at most max of them.
func idList(ids []string, max int) string {
	if len(ids) <= max {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ids[:max], ", "), len(ids)-max)
}

/* ── setting registry ────────────────────────────────────────────────── */

// SettingField is one user-editable Team setting as GET/PUT
// /api/team/settings sees it. Team settings are the home of every
// per-user Team option, so adding one is: a typed column on
// entity.TeamSettings, plus one entry in SettingFields (wire key, how to
// read it, how to validate and set it). The API, the upsert and the
// unknown-key check follow from the registry.
type SettingField struct {
	// Key is the JSON name on the wire.
	Key string
	Get func(st entity.TeamSettings, env SettingEnv) any
	// Set decodes and validates raw into st; an error leaves the save
	// refused (ApplySettings works on a copy).
	Set func(st *entity.TeamSettings, raw json.RawMessage, env SettingEnv) error
}

// SettingEnv is what a setting needs to know about the caller beyond
// their own row. The handler builds it per request so this package keeps
// no http/login imports.
type SettingEnv struct {
	// TicketProject reports whether the caller may put projectID on their
	// Tickets page: it exists, they can open it, and its ticket mode is on.
	// Nil allows no project.
	TicketProject func(projectID string) bool
}

func (e SettingEnv) ticketProject(id string) bool {
	return e.TicketProject != nil && e.TicketProject(id)
}

// MaxTicketProjects caps the Tickets page selection: each one is a board
// the page loads.
const MaxTicketProjects = 50

// visibleTicketProjects is st's selection as the caller may see it now:
// projects they lost access to, or whose ticket mode went off, are left
// out (but stay stored until the next save of this setting).
func visibleTicketProjects(st entity.TeamSettings, env SettingEnv) []string {
	out := []string{}
	for _, id := range st.TicketProjectIDs() {
		if env.ticketProject(id) {
			out = append(out, id)
		}
	}
	return out
}

// setTicketProjects validates a new selection: every id must pass
// env.TicketProject (one that does not is refused, not dropped, so the
// picker never shows a save that did not happen). Duplicates collapse to
// the first, order is kept; an empty list clears the selection.
func setTicketProjects(st *entity.TeamSettings, raw json.RawMessage, env SettingEnv) error {
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return errors.New("ticket_projects must be an array of project ids")
	}
	if len(ids) > 2*MaxTicketProjects {
		return fmt.Errorf("ticket_projects: %d ids, the limit is %d projects", len(ids), MaxTicketProjects)
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	var bad []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if !env.ticketProject(id) {
			bad = append(bad, id)
			continue
		}
		out = append(out, id)
	}
	if len(bad) > 0 {
		return fmt.Errorf("ticket_projects: no such project with ticket mode on that you can open: %s", idList(bad, 5))
	}
	if len(out) > MaxTicketProjects {
		return fmt.Errorf("ticket_projects: %d projects, the limit is %d", len(out), MaxTicketProjects)
	}
	if len(out) == 0 {
		st.TicketProjects = ""
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	st.TicketProjects = string(b)
	return nil
}

// SettingFields lists every Team setting, in display order.
var SettingFields = []SettingField{
	{
		Key: "prompt",
		Get: func(st entity.TeamSettings, _ SettingEnv) any { return st.Prompt },
		Set: func(st *entity.TeamSettings, raw json.RawMessage, _ SettingEnv) error {
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return errors.New("prompt must be a string")
			}
			if err := ValidateTeamPrompt(v); err != nil {
				return err
			}
			st.Prompt = v
			return nil
		},
	},
	{
		Key: "open_team",
		Get: func(st entity.TeamSettings, _ SettingEnv) any { return st.OpenTeam() },
		Set: func(st *entity.TeamSettings, raw json.RawMessage, _ SettingEnv) error {
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				return errors.New("open_team must be true or false")
			}
			st.TeamHome = v
			return nil
		},
	},
	{
		Key: "idle_animations",
		Get: func(st entity.TeamSettings, _ SettingEnv) any { return st.IdleAnimations() },
		Set: func(st *entity.TeamSettings, raw json.RawMessage, _ SettingEnv) error {
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				return errors.New("idle_animations must be true or false")
			}
			st.NoIdleFidget = !v
			return nil
		},
	},
	{
		Key: "ticket_projects",
		Get: func(st entity.TeamSettings, env SettingEnv) any { return visibleTicketProjects(st, env) },
		Set: setTicketProjects,
	},
}

// SettingValues is st as the wire map: one entry per SettingFields key,
// as env's caller may see it.
func SettingValues(st entity.TeamSettings, env SettingEnv) map[string]any {
	out := make(map[string]any, len(SettingFields))
	for _, f := range SettingFields {
		out[f.Key] = f.Get(st, env)
	}
	return out
}

// ApplySettings sets the fields patch names on st. All or nothing: an
// unknown key or one invalid value leaves st unchanged and names every
// problem.
func ApplySettings(st *entity.TeamSettings, patch map[string]json.RawMessage, env SettingEnv) error {
	byKey := make(map[string]SettingField, len(SettingFields))
	for _, f := range SettingFields {
		byKey[f.Key] = f
	}
	next := *st
	var problems []string
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f, ok := byKey[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("unknown setting %q", k))
			continue
		}
		if err := f.Set(&next, patch[k], env); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	*st = next
	return nil
}
