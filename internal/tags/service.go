package tags

import (
	"context"
	"errors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
	"strings"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

type Service struct {
	repo *repo
}

func NewService(db *gorm.DB) *Service {
	return &Service{repo: newRepo(db)}
}

// GroupTags returns tags that should render as groups on the home page,
// ordered by sort_order then name.
func (s *Service) GroupTags(ctx context.Context) ([]*entity.Tag, error) {
	return s.repo.ListGroupTags(ctx)
}

// EnsureToolDefaultTags seeds DefaultTags for a tool on startup. For each
// spec it ensures the global tag exists by name (creating it with the
// declared flags if missing; existing tags are left untouched EXCEPT for
// the IsSystem flag — see below). It then links every spec tag to toolPath
// only when the tool has *no* tool_tag rows yet — so an admin who later
// unlinks a tag won't see it return after a restart.
//
// IsSystem is special: it is code-owned (admins cannot toggle it from UI),
// so this function force-syncs the flag on EXISTING rows whose default
// declares IsSystem=true. That way a tag created before the IsSystem
// schema landed gets backfilled with the flag on the next boot — without
// it, the admin/repo guards (UpdateTag/DeleteTag/SetUserTags) wouldn't
// recognize the row as protected.
func (s *Service) EnsureToolDefaultTags(ctx context.Context, toolPath string, defaults []tool.DefaultTag) error {
	if len(defaults) == 0 {
		return nil
	}
	tagIDs := make([]string, 0, len(defaults))
	for _, d := range defaults {
		name := strings.TrimSpace(d.Name)
		if name == "" {
			continue
		}
		t, err := s.repo.GetTagByName(ctx, name)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			t = &entity.Tag{
				Name:        name,
				Description: strings.TrimSpace(d.Description),
				IsGroup:     d.IsGroup,
				IsFilter:    d.IsFilter,
				IsSystem:    d.IsSystem,
				SortOrder:   d.SortOrder,
			}
			if err := s.repo.CreateTag(ctx, t); err != nil {
				log.Ctx(ctx).Error().Msgf("seed tag %q for %s: %s", name, toolPath, err.Error())
				continue
			}
		} else if err != nil {
			log.Ctx(ctx).Error().Msgf("lookup tag %q for %s: %s", name, toolPath, err.Error())
			continue
		} else if d.IsSystem && !t.IsSystem {
			if err := s.repo.SetIsSystem(ctx, t.ID, true); err != nil {
				log.Ctx(ctx).Error().Msgf("backfill is_system on tag %q: %s", name, err.Error())
			} else {
				t.IsSystem = true
			}
		}
		tagIDs = append(tagIDs, t.ID)
	}
	if len(tagIDs) == 0 {
		return nil
	}
	hasLinks, err := s.repo.HasToolTags(ctx, toolPath)
	if err != nil {
		return err
	}
	if hasLinks {
		return nil
	}
	for _, id := range tagIDs {
		if err := s.repo.LinkToolTag(ctx, toolPath, id); err != nil {
			log.Ctx(ctx).Error().Msgf("link tag %s to %s: %s", id, toolPath, err.Error())
		}
	}
	return nil
}

// TagsByIDs returns Tag rows for the given ids. Used by surfaces that
// have a list of tag ids from ToolTagIDs and want to render names/flags.
func (s *Service) TagsByIDs(ctx context.Context, ids []string) ([]*entity.Tag, error) {
	return s.repo.TagsByIDs(ctx, ids)
}

// SyncSystemTagsForAllAdmins reconciles UserTag rows so every existing
// admin carries every Tag flagged IsSystem. Idempotent — uses
// FirstOrCreate per (user, tag) pair.
//
// Called once at boot (after EnsureToolDefaultTags has had a chance to
// seed System tags) so admins who existed before the System-tag schema
// landed are auto-granted access to System-gated items. Per-user role
// changes after boot are handled inline by admin.Repo.SetRole — this
// boot call only catches the migration-time backfill.
func (s *Service) SyncSystemTagsForAllAdmins(ctx context.Context) error {
	return s.repo.SyncSystemTagsForAllAdmins(ctx)
}

// CreateOwnerTag creates a tag named "owner:{connectorID}" (IsFilter=true),
// links it to the connector row path, and links it to the given userID.
// Idempotent — if the tag already exists the existing tag is reused.
// Call this whenever a new connector instance row is created by a user.
func (s *Service) CreateOwnerTag(ctx context.Context, connectorID, userID string) error {
	name := "owner:" + connectorID
	toolPath := "/connectors/" + connectorID

	t, err := s.repo.GetTagByNameExact(ctx, name)
	if err != nil {
		// Not found — create it.
		t = &entity.Tag{
			Name:     name,
			IsFilter: true,
		}
		if err := s.repo.CreateTag(ctx, t); err != nil {
			return err
		}
	}
	// Link tag → connector row (so the row is visible to whoever carries the tag).
	if err := s.repo.LinkToolTag(ctx, toolPath, t.ID); err != nil {
		return err
	}
	// Link tag → user (owner carries the tag).
	if userID == "" {
		return nil
	}
	return s.repo.LinkUserTag(ctx, userID, t.ID)
}

// DeleteOwnerTag removes the "owner:{connectorID}" tag and all its
// ToolTag + UserTag associations. Call this when a connector instance is deleted.
func (s *Service) DeleteOwnerTag(ctx context.Context, connectorID string) error {
	name := "owner:" + connectorID
	t, err := s.repo.GetTagByNameExact(ctx, name)
	if err != nil {
		return nil // tag doesn't exist — nothing to clean up
	}
	return s.repo.DeleteTag(ctx, t.ID)
}

// UserOwnsConnector reports whether the user carries the owner tag for the
// given connector instance. Used by canConfigureRow / canConnectSSO.
func (s *Service) UserOwnsConnector(ctx context.Context, userID, connectorID string) (bool, error) {
	name := "owner:" + connectorID
	t, err := s.repo.GetTagByNameExact(ctx, name)
	if err != nil {
		return false, nil // no owner tag = not owned
	}
	return s.repo.UserCarriesTag(ctx, userID, t.ID)
}

// CreateResourceOwnerTag creates an owner tag for a generic resource
// (project, workflow, skill). Unlike CreateOwnerTag it does NOT link
// a tool-path row — it only creates the tag and links it to the user.
// The tag name is "owner:{resourceID}". Idempotent.
func (s *Service) CreateResourceOwnerTag(ctx context.Context, resourceID, userID string) error {
	if resourceID == "" || userID == "" {
		return nil
	}
	name := "owner:" + resourceID
	t, err := s.repo.GetTagByNameExact(ctx, name)
	if err != nil {
		t = &entity.Tag{Name: name, IsFilter: true}
		if err := s.repo.CreateTag(ctx, t); err != nil {
			return err
		}
	}
	return s.repo.LinkUserTag(ctx, userID, t.ID)
}

// UserOwnsResource reports whether the user carries the owner tag for
// the given resource ID. Returns false (not error) when tag doesn't exist.
func (s *Service) UserOwnsResource(ctx context.Context, userID, resourceID string) (bool, error) {
	if resourceID == "" || userID == "" {
		return false, nil
	}
	name := "owner:" + resourceID
	t, err := s.repo.GetTagByNameExact(ctx, name)
	if err != nil {
		return false, nil
	}
	return s.repo.UserCarriesTag(ctx, userID, t.ID)
}

// DeleteResourceOwnerTag removes the owner tag for the resource and all
// its UserTag associations. Safe to call when tag doesn't exist.
func (s *Service) DeleteResourceOwnerTag(ctx context.Context, resourceID string) error {
	if resourceID == "" {
		return nil
	}
	name := "owner:" + resourceID
	t, err := s.repo.GetTagByNameExact(ctx, name)
	if err != nil {
		return nil
	}
	return s.repo.DeleteTag(ctx, t.ID)
}

// ToolTagIDs returns a map from tool_path to the list of tag ids it has.
func (s *Service) ToolTagIDs(ctx context.Context, toolPaths []string) (map[string][]string, error) {
	out := make(map[string][]string)
	if len(toolPaths) == 0 {
		return out, nil
	}
	rows, err := s.repo.ListToolTags(ctx, toolPaths)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ToolPath] = append(out[r.ToolPath], r.TagID)
	}
	return out, nil
}
