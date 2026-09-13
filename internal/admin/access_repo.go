package admin

import (
	"context"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// ── Access reach queries ──────────────────────────────────────────────────
//
// All of these read the same two joins — tool_tags → tags(is_filter) →
// user_tags → users — because that IS the access rule every surface applies
// (see Repo.ListAccessibleTo in internal/connectors and its siblings). Kept
// here as one set so the numbers the admin panel shows cannot drift from the
// rule the server enforces.

// ApprovedUserCount is the size of "everyone" — the reach of an item with no
// filter tags. Unapproved users cannot log in, so they are not part of it.
func (r *repo) ApprovedUserCount(ctx context.Context) int {
	var n int64
	r.db.WithContext(ctx).Model(&entity.User{}).Where("approved = ?", true).Count(&n)
	return int(n)
}

// AccessUserCounts returns, per tool_path, how many APPROVED users carry at
// least one of that path's filter tags. A path absent from the result carries
// no filter tag at all — it is public, and the caller renders it as such.
//
// One query for the whole page: the admin lists are 40–130 rows and an N+1
// here would be felt.
func (r *repo) AccessUserCounts(ctx context.Context, paths []string) (map[string]int, error) {
	ids, err := r.AccessUserIDs(ctx, paths)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(ids))
	for path, set := range ids {
		out[path] = len(set)
	}
	return out, nil
}

// AccessUserIDs is AccessUserCounts before it collapses to a number: per
// tool_path, the SET of approved users carrying one of its filter tags. A
// path absent from the result carries no filter tag at all — it is public.
//
// Sets rather than counts because the caller unions the admin bypass into
// them, and an admin who also carries the tag must be counted once.
func (r *repo) AccessUserIDs(ctx context.Context, paths []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	if len(paths) == 0 {
		return out, nil
	}
	// Every path that has ≥1 filter tag, so a tagged-but-unreachable row
	// (empty set) is still reported as restricted rather than as public.
	var tagged []struct{ ToolPath string }
	if err := r.db.WithContext(ctx).
		Table("tool_tags tt").
		Select("DISTINCT tt.tool_path as tool_path").
		Joins("JOIN tags t ON t.id = tt.tag_id").
		Where("tt.tool_path IN ?", paths).
		Where("t.is_filter = ?", true).
		Scan(&tagged).Error; err != nil {
		return nil, err
	}
	for _, row := range tagged {
		out[row.ToolPath] = map[string]bool{}
	}

	var pairs []struct {
		ToolPath string
		UserID   string
	}
	if err := r.db.WithContext(ctx).
		Table("tool_tags tt").
		Select("DISTINCT tt.tool_path as tool_path, CAST(ut.user_id AS TEXT) as user_id").
		Joins("JOIN tags t ON t.id = tt.tag_id").
		Joins("JOIN user_tags ut ON ut.tag_id = tt.tag_id").
		Joins("JOIN users u ON CAST(u.id AS TEXT) = CAST(ut.user_id AS TEXT)").
		Where("tt.tool_path IN ?", paths).
		Where("t.is_filter = ?", true).
		Where("u.approved = ?", true).
		Scan(&pairs).Error; err != nil {
		return nil, err
	}
	for _, row := range pairs {
		if out[row.ToolPath] == nil {
			out[row.ToolPath] = map[string]bool{}
		}
		out[row.ToolPath][row.UserID] = true
	}
	return out, nil
}

// AccessDetail lists the people who reach one item, and the tag that let each
// of them in. A public item (no filter tags) lists every approved user with no
// via-tag — that is the honest answer to "who can see this".
func (r *repo) AccessDetail(ctx context.Context, path string) (AccessDetail, error) {
	out := AccessDetail{Path: path}

	var tagRows []struct {
		ID   string
		Name string
	}
	if err := r.db.WithContext(ctx).
		Table("tool_tags tt").
		Select("t.id as id, t.name as name").
		Joins("JOIN tags t ON t.id = tt.tag_id").
		Where("tt.tool_path = ?", path).
		Where("t.is_filter = ?", true).
		Scan(&tagRows).Error; err != nil {
		return out, err
	}
	for _, t := range tagRows {
		out.Tags = append(out.Tags, t.Name)
	}
	sort.Strings(out.Tags)

	if len(tagRows) == 0 {
		out.Public = true
		users, err := r.approvedUsers(ctx)
		if err != nil {
			return out, err
		}
		out.Users = users
		return out, nil
	}

	tagIDs := make([]string, 0, len(tagRows))
	for _, t := range tagRows {
		tagIDs = append(tagIDs, t.ID)
	}
	users, err := r.usersCarryingTags(ctx, tagIDs)
	if err != nil {
		return out, err
	}
	out.Users = users
	return out, nil
}

// approvedUsers is the "public" reach list.
func (r *repo) approvedUsers(ctx context.Context) ([]adminUser, error) {
	var rows []entity.User
	if err := r.db.WithContext(ctx).
		Where("approved = ?", true).
		Order("name asc, email asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]adminUser, 0, len(rows))
	for _, u := range rows {
		out = append(out, adminUser{
			ID: u.ID, Name: u.Name, Email: u.Email,
			Role: string(u.Role), Approved: u.Approved,
		})
	}
	return out, nil
}

// usersCarryingTags lists approved users holding any of the given tags, each
// annotated with WHICH of them matched — the answer to "why can they see it".
func (r *repo) usersCarryingTags(ctx context.Context, tagIDs []string) ([]adminUser, error) {
	if len(tagIDs) == 0 {
		return nil, nil
	}
	var rows []struct {
		ID       string
		Name     string
		Email    string
		Role     string
		Approved bool
		TagName  string
	}
	if err := r.db.WithContext(ctx).
		Table("user_tags ut").
		Select("u.id as id, u.name as name, u.email as email, u.role as role, u.approved as approved, t.name as tag_name").
		Joins("JOIN users u ON CAST(u.id AS TEXT) = CAST(ut.user_id AS TEXT)").
		Joins("JOIN tags t ON t.id = ut.tag_id").
		Where("ut.tag_id IN ?", tagIDs).
		Where("u.approved = ?", true).
		Order("u.name asc, u.email asc").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	byID := map[string]*adminUser{}
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		u, ok := byID[row.ID]
		if !ok {
			byID[row.ID] = &adminUser{
				ID: row.ID, Name: row.Name, Email: row.Email,
				Role: row.Role, Approved: row.Approved,
				ViaTags: []string{row.TagName},
			}
			order = append(order, row.ID)
			continue
		}
		u.ViaTags = append(u.ViaTags, row.TagName)
	}
	out := make([]adminUser, 0, len(order))
	for _, id := range order {
		u := byID[id]
		sort.Strings(u.ViaTags)
		out = append(out, *u)
	}
	return out, nil
}

// TagUsageCounts returns per-tag counters for the Tags page: how many approved
// users carry it, and how many items it gates. Two aggregate queries, not one
// per tag.
func (r *repo) TagUsageCounts(ctx context.Context) (map[string]TagUsage, error) {
	out := map[string]TagUsage{}

	var userRows []struct {
		TagID string
		N     int
	}
	if err := r.db.WithContext(ctx).
		Table("user_tags ut").
		Select("ut.tag_id as tag_id, COUNT(DISTINCT ut.user_id) as n").
		Joins("JOIN users u ON CAST(u.id AS TEXT) = CAST(ut.user_id AS TEXT)").
		Where("u.approved = ?", true).
		Group("ut.tag_id").
		Scan(&userRows).Error; err != nil {
		return nil, err
	}
	for _, row := range userRows {
		u := out[row.TagID]
		u.TagID = row.TagID
		u.UserCount = row.N
		out[row.TagID] = u
	}

	var itemRows []struct {
		TagID string
		N     int
	}
	if err := r.db.WithContext(ctx).
		Table("tool_tags").
		Select("tag_id, COUNT(DISTINCT tool_path) as n").
		Group("tag_id").
		Scan(&itemRows).Error; err != nil {
		return nil, err
	}
	for _, row := range itemRows {
		u := out[row.TagID]
		u.TagID = row.TagID
		u.ItemCount = row.N
		out[row.TagID] = u
	}
	return out, nil
}

// TagUsageDetail is the modal behind those counters: the people carrying the
// tag and every item it opens.
func (r *repo) TagUsageDetail(ctx context.Context, tagID string) (TagUsage, error) {
	out := TagUsage{TagID: tagID}

	var tag entity.Tag
	if err := r.db.WithContext(ctx).Where("id = ?", tagID).First(&tag).Error; err != nil {
		return out, err
	}
	out.TagName = tag.Name

	users, err := r.usersCarryingTags(ctx, []string{tagID})
	if err != nil {
		return out, err
	}
	out.Users = users
	out.UserCount = len(users)

	var paths []struct{ ToolPath string }
	if err := r.db.WithContext(ctx).
		Table("tool_tags").
		Select("DISTINCT tool_path as tool_path").
		Where("tag_id = ?", tagID).
		Scan(&paths).Error; err != nil {
		return out, err
	}
	for _, p := range paths {
		out.Items = append(out.Items, AccessItem{Path: p.ToolPath, Label: accessItemLabel(p.ToolPath)})
	}
	out.ItemCount = len(out.Items)
	return out, nil
}

// accessItemLabel turns a tool_path into the name an admin recognises — the
// last segment, which is the key/slug on every surface except connector rows
// (a uuid, where the path is all we have without loading each service).
func accessItemLabel(path string) string {
	trimmed := strings.TrimSuffix(path, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 && i+1 < len(trimmed) {
		return trimmed[i+1:]
	}
	return trimmed
}

// AdminUserByID resolves one user for the access modals. Returns (nil, nil)
// when the id is unknown — a stale owner id on a row is not an error worth
// failing the page for.
func (r *repo) AdminUserByID(ctx context.Context, id string) (*adminUser, error) {
	if id == "" {
		return nil, nil
	}
	var u entity.User
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&u).Error; err != nil {
		return nil, nil
	}
	return &adminUser{
		ID: u.ID, Name: u.Name, Email: u.Email,
		Role: string(u.Role), Approved: u.Approved,
	}, nil
}

// AdminUsers lists the admin accounts, for the "admins also see this" part of
// an account's reach.
func (r *repo) AdminUsers(ctx context.Context) ([]adminUser, error) {
	var rows []entity.User
	if err := r.db.WithContext(ctx).
		Where("role = ? OR is_owner = ?", entity.RoleAdmin, true).
		Where("approved = ?", true).
		Order("name asc, email asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]adminUser, 0, len(rows))
	for _, u := range rows {
		out = append(out, adminUser{
			ID: u.ID, Name: u.Name, Email: u.Email,
			Role: string(u.Role), Approved: u.Approved,
		})
	}
	return out, nil
}

// OwnerTagHolders resolves who carries each "owner:<id>" tag — the grant an
// admin uses to hand a project, workflow, skill or data table to somebody
// who did not create it. Keyed by tag NAME, since that is what the caller
// builds from the resource id.
func (r *repo) OwnerTagHolders(ctx context.Context, tagNames []string) (map[string][]adminUser, error) {
	out := map[string][]adminUser{}
	if len(tagNames) == 0 {
		return out, nil
	}
	var rows []struct {
		TagName  string
		ID       string
		Name     string
		Email    string
		Role     string
		Approved bool
	}
	if err := r.db.WithContext(ctx).
		Table("tags t").
		Select("t.name as tag_name, u.id as id, u.name as name, u.email as email, u.role as role, u.approved as approved").
		Joins("JOIN user_tags ut ON ut.tag_id = t.id").
		Joins("JOIN users u ON CAST(u.id AS TEXT) = CAST(ut.user_id AS TEXT)").
		Where("t.name IN ?", tagNames).
		Where("u.approved = ?", true).
		Order("u.name asc, u.email asc").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.TagName] = append(out[row.TagName], adminUser{
			ID: row.ID, Name: row.Name, Email: row.Email,
			Role: row.Role, Approved: row.Approved,
		})
	}
	return out, nil
}
