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
// declared flags if missing; existing tags are left untouched). It then
// links every spec tag to toolPath only when the tool has *no* tool_tag
// rows yet — so an admin who later unlinks a tag won't see it return
// after a restart.
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
				SortOrder:   d.SortOrder,
			}
			if err := s.repo.CreateTag(ctx, t); err != nil {
				log.Ctx(ctx).Error().Msgf("seed tag %q for %s: %s", name, toolPath, err.Error())
				continue
			}
		} else if err != nil {
			log.Ctx(ctx).Error().Msgf("lookup tag %q for %s: %s", name, toolPath, err.Error())
			continue
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
