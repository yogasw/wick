package bookmark

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

type Service struct {
	repo *repo
}

func NewService(db *gorm.DB) *Service {
	return &Service{repo: newRepo(db)}
}

// Toggle adds or removes a bookmark and reports the new state.
func (s *Service) Toggle(ctx context.Context, userID, toolPath string) (bookmarked bool, err error) {
	_, err = s.repo.Find(ctx, userID, toolPath)
	if err == nil {
		return false, s.repo.Delete(ctx, userID, toolPath)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if err := s.repo.Create(ctx, userID, toolPath); err != nil {
		return false, err
	}
	return true, nil
}

// ListForUser returns the set of tool paths bookmarked by the user.
func (s *Service) ListForUser(ctx context.Context, userID string) (map[string]bool, error) {
	if userID == "" {
		return map[string]bool{}, nil
	}
	rows, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, b := range rows {
		out[b.ToolPath] = true
	}
	return out, nil
}
