package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/sso"
)

// manage_api.go is the Users & tags page as calls, for the wick manager
// connector (MCP). Every method mirrors one admin form so both paths keep
// the same rules (last admin, system tags, approval before tags, notices).
// The caller is responsible for the admin gate.

// ManageUser is one account as the manager ops return it.
type ManageUser struct {
	ID        string   `json:"id"`
	Email     string   `json:"email"`
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	IsOwner   bool     `json:"is_owner,omitempty"`
	Approved  bool     `json:"approved"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
}

// ManageListUsers lists accounts. status: pending, approved or "" (all).
// query narrows by part of the name or email.
func (h *Handler) ManageListUsers(ctx context.Context, status, query string) ([]ManageUser, error) {
	users, err := h.repo.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	tags, err := h.repo.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	tagName := make(map[string]string, len(tags))
	for _, t := range tags {
		tagName[t.ID] = t.Name
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := []ManageUser{}
	for _, u := range users {
		switch status {
		case "pending":
			if u.Approved {
				continue
			}
		case "approved":
			if !u.Approved {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(u.Name+" "+u.Email), query) {
			continue
		}
		ids, _ := h.repo.GetUserTagIDs(ctx, u.ID)
		names := []string{}
		for _, id := range ids {
			if n, ok := tagName[id]; ok && !strings.HasPrefix(n, "owner:") {
				names = append(names, n)
			}
		}
		out = append(out, ManageUser{
			ID: u.ID, Email: u.Email, Name: u.Name, Role: string(u.Role), IsOwner: u.IsOwner,
			Approved: u.Approved, Tags: names, CreatedAt: u.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return out, nil
}

// ErrUserNotFound is returned when no account matches the id or email.
var ErrUserNotFound = errors.New("user not found (pass the user id or exact email)")

// resolveUser finds an account by id or email (case-insensitive).
func (h *Handler) resolveUser(ctx context.Context, ref string) (*entity.User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrUserNotFound
	}
	users, err := h.repo.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.ID == ref || strings.EqualFold(u.Email, ref) {
			return u, nil
		}
	}
	return nil, ErrUserNotFound
}

// ManageSetApproved approves or revokes an account. Approving sends the same
// notices as the Users page button.
func (h *Handler) ManageSetApproved(ctx context.Context, userRef string, approved bool) (*entity.User, error) {
	u, err := h.resolveUser(ctx, userRef)
	if err != nil {
		return nil, err
	}
	if err := h.repo.SetApproved(ctx, u.ID, approved); err != nil {
		return nil, err
	}
	if approved && !u.Approved && h.onUserApproved != nil {
		h.onUserApproved(ctx, u.ID)
	}
	u.Approved = approved
	return u, nil
}

// ManageSetRole sets admin or user. Demoting the last admin is refused.
func (h *Handler) ManageSetRole(ctx context.Context, userRef, role string) (*entity.User, error) {
	r := entity.UserRole(strings.TrimSpace(role))
	if r != entity.RoleAdmin && r != entity.RoleUser {
		return nil, fmt.Errorf("invalid role %q (admin or user)", role)
	}
	u, err := h.resolveUser(ctx, userRef)
	if err != nil {
		return nil, err
	}
	if err := h.repo.SetRole(ctx, u.ID, r); err != nil {
		return nil, err
	}
	u.Role = r
	return u, nil
}

// ManageSetUserTags changes an account's tags. set replaces the list (system
// tags kept); otherwise add / remove edit it. Tags are given by name or id.
func (h *Handler) ManageSetUserTags(ctx context.Context, userRef string, set, add, remove []string, replace bool) ([]string, error) {
	u, err := h.resolveUser(ctx, userRef)
	if err != nil {
		return nil, err
	}
	tags, err := h.repo.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	byRef := map[string]*entity.Tag{}
	for _, t := range tags {
		byRef[t.ID] = t
		byRef[strings.ToLower(t.Name)] = t
	}
	resolve := func(refs []string) ([]string, error) {
		ids := []string{}
		for _, ref := range refs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			t, ok := byRef[ref]
			if !ok {
				t, ok = byRef[strings.ToLower(ref)]
			}
			if !ok {
				return nil, fmt.Errorf("unknown tag %q", ref)
			}
			ids = append(ids, t.ID)
		}
		return ids, nil
	}
	current := map[string]bool{}
	if !replace {
		ids, err := h.repo.GetUserTagIDs(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if t, ok := byRef[id]; ok && !t.IsSystem {
				current[id] = true
			}
		}
	}
	setIDs, err := resolve(set)
	if err != nil {
		return nil, err
	}
	addIDs, err := resolve(add)
	if err != nil {
		return nil, err
	}
	removeIDs, err := resolve(remove)
	if err != nil {
		return nil, err
	}
	for _, id := range setIDs {
		current[id] = true
	}
	for _, id := range addIDs {
		current[id] = true
	}
	for _, id := range removeIDs {
		delete(current, id)
	}
	final := make([]string, 0, len(current))
	names := make([]string, 0, len(current))
	for _, t := range tags { // tag order, so the answer is stable
		if current[t.ID] {
			final = append(final, t.ID)
			names = append(names, t.Name)
		}
	}
	if err := h.repo.SetUserTags(ctx, u.ID, final); err != nil {
		return nil, err
	}
	return names, nil
}

// ManageListTags lists tags with how many people carry each. Owner tags
// (one per resource owner) are left out, as on the Tags page.
func (h *Handler) ManageListTags(ctx context.Context) ([]map[string]any, error) {
	tags, err := h.repo.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	counts, _ := h.repo.TagUsageCounts(ctx)
	out := []map[string]any{}
	for _, t := range filterOutOwnerTags(tags) {
		m := tagToMap(t)
		m["is_system"] = t.IsSystem
		if c, ok := counts[t.ID]; ok {
			m["users"] = c.UserCount
			m["items"] = c.ItemCount
		}
		out = append(out, m)
	}
	return out, nil
}

// ManageCreateTag creates a tag.
func (h *Handler) ManageCreateTag(ctx context.Context, name, description string, isGroup, isFilter bool) (map[string]any, error) {
	t, err := h.repo.CreateTag(ctx, name, isGroup, isFilter)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(description) != "" {
		if err := h.repo.UpdateTag(ctx, t.ID, t.Name, description, isGroup, isFilter, t.SortOrder); err != nil {
			return nil, err
		}
		t.Description = description
	}
	return tagToMap(t), nil
}

// TagPatch is a partial tag update: nil fields keep their value.
type TagPatch struct {
	Name        *string
	Description *string
	IsGroup     *bool
	IsFilter    *bool
	SortOrder   *int
}

// ManageUpdateTag updates a tag found by id or name.
func (h *Handler) ManageUpdateTag(ctx context.Context, ref string, p TagPatch) (map[string]any, error) {
	t, err := h.findTag(ctx, ref)
	if err != nil {
		return nil, err
	}
	if p.Name != nil {
		t.Name = *p.Name
	}
	if p.Description != nil {
		t.Description = *p.Description
	}
	if p.IsGroup != nil {
		t.IsGroup = *p.IsGroup
	}
	if p.IsFilter != nil {
		t.IsFilter = *p.IsFilter
	}
	if p.SortOrder != nil {
		t.SortOrder = *p.SortOrder
	}
	if err := h.repo.UpdateTag(ctx, t.ID, t.Name, t.Description, t.IsGroup, t.IsFilter, t.SortOrder); err != nil {
		return nil, err
	}
	return tagToMap(t), nil
}

// ManageDeleteTag deletes a tag found by id or name.
func (h *Handler) ManageDeleteTag(ctx context.Context, ref string) (string, error) {
	t, err := h.findTag(ctx, ref)
	if err != nil {
		return "", err
	}
	if err := h.repo.DeleteTag(ctx, t.ID); err != nil {
		return "", err
	}
	return t.Name, nil
}

func (h *Handler) findTag(ctx context.Context, ref string) (*entity.Tag, error) {
	ref = strings.TrimSpace(ref)
	tags, err := h.repo.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tags {
		if t.ID == ref || strings.EqualFold(t.Name, ref) {
			return t, nil
		}
	}
	return nil, fmt.Errorf("unknown tag %q", ref)
}

// SignupSettings is the sign-up part of the Google SSO provider.
type SignupSettings struct {
	Provider           string `json:"provider"`
	Enabled            bool   `json:"enabled"`
	AutoRegister       bool   `json:"auto_register"`
	AllowedDomains     string `json:"allowed_domains"`
	AutoApproveDomains string `json:"auto_approve_domains"`
}

// SignupPatch changes sign-up settings; nil fields keep their value.
type SignupPatch struct {
	AutoRegister       *bool
	AllowedDomains     *string
	AutoApproveDomains *string
}

// ManageGetSignup reads the Google provider's sign-up settings.
func (h *Handler) ManageGetSignup() (SignupSettings, error) {
	p, ok := h.sso.Get(entity.SSOProviderGoogle)
	if !ok {
		return SignupSettings{}, errors.New("google SSO provider not found")
	}
	return SignupSettings{
		Provider: p.Provider, Enabled: p.Enabled, AutoRegister: !p.DisableAutoRegister,
		AllowedDomains: p.AllowedDomains, AutoApproveDomains: p.AutoApproveDomains,
	}, nil
}

// ManageSetSignup changes sign-up settings and keeps the OAuth client fields.
func (h *Handler) ManageSetSignup(ctx context.Context, p SignupPatch) (SignupSettings, error) {
	cur, ok := h.sso.Get(entity.SSOProviderGoogle)
	if !ok {
		return SignupSettings{}, errors.New("google SSO provider not found")
	}
	in := sso.UpdateInput{
		ClientID: cur.ClientID, ClientSecret: cur.ClientSecret, Enabled: cur.Enabled,
		AllowedDomains: cur.AllowedDomains, AutoApproveDomains: cur.AutoApproveDomains,
		DisableAutoRegister: cur.DisableAutoRegister,
	}
	if p.AutoRegister != nil {
		in.DisableAutoRegister = !*p.AutoRegister
	}
	if p.AllowedDomains != nil {
		in.AllowedDomains = *p.AllowedDomains
	}
	if p.AutoApproveDomains != nil {
		in.AutoApproveDomains = *p.AutoApproveDomains
	}
	if err := h.sso.Update(ctx, entity.SSOProviderGoogle, in); err != nil {
		return SignupSettings{}, err
	}
	return h.ManageGetSignup()
}
