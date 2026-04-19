package login

import (
	"context"
	"errors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/ui"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct {
	repo        *repo
	adminEmails map[string]bool
}

func NewService(db *gorm.DB, adminEmailsCSV string) *Service {
	adminMap := make(map[string]bool)
	for _, e := range strings.Split(adminEmailsCSV, ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			adminMap[e] = true
		}
	}
	return &Service{repo: newRepo(db), adminEmails: adminMap}
}

func (s *Service) UpsertUser(ctx context.Context, email, name, avatar string) (*entity.User, error) {
	return s.repo.UpsertUser(ctx, strings.ToLower(email), name, avatar, s.adminEmails)
}

func (s *Service) GetUserByID(ctx context.Context, id string) (*entity.User, error) {
	return s.repo.GetUserByID(ctx, id)
}

// GetUserFilterTagIDs fetches the filter-type tag IDs for a user.
// Called at login time; the result is embedded in the encrypted session cookie
// so subsequent requests do not need an extra DB query for tag matching.
func (s *Service) GetUserFilterTagIDs(ctx context.Context, userID string) []string {
	return s.repo.GetUserFilterTagIDs(ctx, userID)
}

var ErrInvalidCredentials = errors.New("invalid email or password")

func (s *Service) LoginWithPassword(ctx context.Context, email, password string) (*entity.User, error) {
	u, err := s.repo.GetUserByEmail(ctx, strings.ToLower(email))
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if u.PasswordHash == "" {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

// SetTheme updates the user's UI theme preference. Empty id means
// "no preference" and the UI falls back to the device system setting.
func (s *Service) SetTheme(ctx context.Context, userID, themeID string) error {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	meta := u.Metadata
	meta.Theme = themeID
	if t := ui.ThemeByID(themeID); t.ID != "" {
		if t.IsDark {
			meta.DarkTheme = t.ID
		} else {
			meta.LightTheme = t.ID
		}
	}
	return s.repo.SetMetadata(ctx, userID, meta)
}

// SetHomeView updates the user's home-grid view preference.
func (s *Service) SetHomeView(ctx context.Context, userID, view string) error {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	meta := u.Metadata
	switch view {
	case entity.HomeViewDetailed:
		meta.HomeView = entity.HomeViewDetailed
	default:
		meta.HomeView = entity.HomeViewCompact
	}
	return s.repo.SetMetadata(ctx, userID, meta)
}

func (s *Service) SetPassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	// If a password is already set, verify the current one first.
	if u.PasswordHash != "" {
		if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(currentPassword)); err != nil {
			return errors.New("current password is incorrect")
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.repo.SetPasswordHash(ctx, userID, string(hash))
}

// BootstrapAdmin is a one-shot seed: if no user with the admin role
// exists yet, it creates one account per configured admin email and
// sets the given password on it. When at least one admin is already
// present the whole thing is a no-op — so the seed can't resurrect
// deleted admins or overwrite a live password.
func (s *Service) BootstrapAdmin(ctx context.Context, defaultPassword string) {
	n, err := s.repo.CountAdmins(ctx)
	if err != nil || n > 0 {
		return
	}
	if defaultPassword == "" {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultPassword), bcrypt.DefaultCost)
	if err != nil {
		return
	}
	for email := range s.adminEmails {
		name := email
		if i := strings.Index(email, "@"); i > 0 {
			name = strings.ToUpper(email[:1]) + email[1:i]
		}
		u, err := s.repo.UpsertUser(ctx, email, name, "", s.adminEmails)
		if err != nil {
			continue
		}
		_ = s.repo.SetPasswordHash(ctx, u.ID, string(hash))
	}
}

func (s *Service) CanAccessTool(ctx context.Context, user *entity.User, toolPath string, defaultVis entity.ToolVisibility) bool {
	vis, disabled := s.repo.GetToolPerm(ctx, toolPath, defaultVis)
	if disabled {
		// Disabled means hidden for everyone, admins included. Admins
		// manage the flag from /admin/tools.
		return false
	}
	if vis == entity.VisibilityPublic {
		return true
	}
	// Private: require approved login. If the tool has required tags,
	// the user must carry at least one of them (admins bypass).
	if user == nil || !user.Approved {
		return false
	}
	if user.IsAdmin() {
		return true
	}
	filterTagIDs := s.repo.GetToolFilterTagIDs(ctx, toolPath)
	if len(filterTagIDs) == 0 {
		return true
	}
	for _, uid := range GetUserTagIDs(ctx) {
		for _, fid := range filterTagIDs {
			if uid == fid {
				return true
			}
		}
	}
	return false
}
