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
	return r.access(ctx).totalApproved
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
	d := r.access(ctx)
	for _, p := range paths {
		tagIDs, tagged := d.pathTags[p]
		if !tagged {
			continue // no filter tag: the caller reads that as public
		}
		set := map[string]bool{}
		for _, tid := range tagIDs {
			for uid := range d.tagHolders[tid] {
				set[uid] = true
			}
		}
		out[p] = set
	}
	return out, nil
}

// AccessDetail lists the people who reach one item, and the tag that let each
// of them in. A public item (no filter tags) lists every approved user with no
// via-tag — that is the honest answer to "who can see this".
func (r *repo) AccessDetail(ctx context.Context, path string) (AccessDetail, error) {
	out := AccessDetail{Path: path}
	d := r.access(ctx)

	tagIDs, tagged := d.pathTags[path]
	if !tagged {
		out.Public = true
		out.Users = append([]adminUser(nil), d.users...)
		return out, nil
	}
	for _, id := range tagIDs {
		out.Tags = append(out.Tags, d.tagNames[id])
	}
	sort.Strings(out.Tags)
	out.Users = d.holdersOf(tagIDs)
	return out, nil
}

// holdersOf lists the approved users carrying any of these tags, each
// annotated with WHICH of them matched — the answer to "why can they see it".
// Reads the same cached sets the badge counts from, so the modal can never
// disagree with the number that opened it.
func (d *accessData) holdersOf(tagIDs []string) []adminUser {
	reasons := map[string][]string{}
	for _, tid := range tagIDs {
		for uid := range d.tagHolders[tid] {
			reasons[uid] = append(reasons[uid], d.tagNames[tid])
		}
	}
	out := make([]adminUser, 0, len(reasons))
	for _, u := range d.users { // name order, from the cached listing
		via, ok := reasons[u.ID]
		if !ok {
			continue
		}
		sort.Strings(via)
		u.ViaTags = via
		out = append(out, u)
	}
	return out
}

// approvedUsers is the "public" reach list, from the cache.
func (r *repo) approvedUsers(ctx context.Context) ([]adminUser, error) {
	return append([]adminUser(nil), r.access(ctx).users...), nil
}

// usersCarryingTags is holdersOf against the cached sets.
func (r *repo) usersCarryingTags(ctx context.Context, tagIDs []string) ([]adminUser, error) {
	if len(tagIDs) == 0 {
		return nil, nil
	}
	return r.access(ctx).holdersOf(tagIDs), nil
}

// TagUsageCounts returns per-tag counters for the Tags page: how many approved
// users carry it, and how many items it gates. Two aggregate queries, not one
// per tag.
func (r *repo) TagUsageCounts(ctx context.Context) (map[string]TagUsage, error) {
	d := r.access(ctx)
	out := make(map[string]TagUsage, len(d.tagNames))
	for tagID, holders := range d.tagHolders {
		u := out[tagID]
		u.TagID = tagID
		u.UserCount = len(holders)
		out[tagID] = u
	}
	// Item counts cover every tag assignment, including the non-filter
	// (category) tags the cache does not carry — those group things on the
	// connectors index and an admin still wants to see how many they label.
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
	for _, u := range r.access(ctx).users {
		if u.ID == id {
			cp := u
			return &cp, nil
		}
	}
	return nil, nil
}

// AdminUsers lists the admin accounts, for the "admins also see this" part of
// an account's reach.
func (r *repo) AdminUsers(ctx context.Context) ([]adminUser, error) {
	return r.access(ctx).admins, nil
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
	d := r.access(ctx)
	want := make(map[string]bool, len(tagNames))
	for _, n := range tagNames {
		want[n] = true
	}
	// tagNames maps id → name; walk it once and keep the ids we were asked for.
	for tagID, name := range d.tagNames {
		if !want[name] {
			continue
		}
		out[name] = append(out[name], d.holdersOf([]string{tagID})...)
	}
	return out, nil
}

// ApprovedUserIDs is the id set of everyone who can log in. Used to decide
// whether an implicit grant (the person who connected an account, a row's
// creator) is still a real user — a deactivated account is not reach.
func (r *repo) ApprovedUserIDs(ctx context.Context) (map[string]bool, error) {
	return r.access(ctx).approved, nil
}
