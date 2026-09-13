package admin

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	adminview "github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/pkg/adminscope"
	"github.com/yogasw/wick/internal/entity"
)

// Access reach — "who can actually see this thing".
//
// Every tag-gated surface in the admin panel (tools, jobs, connectors and
// their accounts, projects, workflows, skills, data tables, providers) hangs
// its access tags off the same tool_tags table, so the answer to "who reaches
// this row" is one shape for all of them:
//
//   - no FILTER tag on the row  → PUBLIC: every approved user reaches it
//   - ≥1 filter tag             → RESTRICTED: only users carrying one of them
//
// The number is what an admin actually wants on screen — "restricted" with no
// hint of how many people that is reads the same whether it means two people
// or the whole company, and a restricted row that reaches ZERO users is
// almost always a mistake nobody notices.

// accessKinds maps the tool_path namespace to a human label, used by the tag
// usage breakdown. Paths outside this map fall back to the raw namespace.
var accessKinds = map[string]string{
	"tools":              "Tools",
	"jobs":               "Jobs",
	"connectors":         "Connectors",
	"connector-accounts": "Connected accounts",
	"manager":            "Connector types",
	"projects":           "Projects",
	"workflows":          "Workflows",
	"skills":             "Skills",
	"data-tables":        "Data Tables",
	"providers":          "Providers",
}

// accessKindsOne is the SINGULAR of each label, for prose: the modal says
// "no access tag on this tool", not "on this tools".
var accessKindsOne = map[string]string{
	"tools":              "tool",
	"jobs":               "job",
	"connectors":         "connector",
	"connector-accounts": "connected account",
	"manager":            "connector type",
	"projects":           "project",
	"workflows":          "workflow",
	"skills":             "skill",
	"data-tables":        "data table",
	"providers":          "provider",
}

// accessKindOneOf is accessKindOf in the singular, for a sentence.
func accessKindOneOf(path string) string {
	ns := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
	if label, ok := accessKindsOne[ns]; ok {
		return label
	}
	return "item"
}

// accessKindOf names the surface a tool_path belongs to ("/jobs/foo" → Jobs).
func accessKindOf(path string) string {
	ns := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
	if label, ok := accessKinds[ns]; ok {
		return label
	}
	if ns == "" {
		return "Other"
	}
	return ns
}

// accessSummaries resolves the reach of every given path in one round trip and
// returns it keyed by path, ready to hand to adminview.AccessBadge. Paths with
// no row in the map are public by definition (no tags), so callers can index
// the map directly and get the right zero value.
func (h *Handler) accessSummaries(ctx context.Context, paths []string) map[string]adminview.AccessSummary {
	out := make(map[string]adminview.AccessSummary, len(paths))
	total := h.repo.ApprovedUserCount(ctx)
	sets, err := h.repo.AccessUserIDs(ctx, paths)
	if err != nil {
		// A failed count must not blank the page it decorates: fall back to
		// "unknown" badges rather than dropping the whole listing.
		for _, p := range paths {
			out[p] = adminview.AccessSummary{Path: p, Unknown: true}
		}
		return out
	}
	// Admins are loaded once, then unioned into every path whose surface lets
	// the admin role past the tags. Union, not addition: an admin carrying the
	// tag is one person, not two.
	admins, adminErr := h.repo.AdminUsers(ctx)
	for _, p := range paths {
		set, tagged := sets[p]
		if !tagged {
			out[p] = adminview.AccessSummary{Path: p, Public: true, TotalUser: total}
			continue
		}
		reach := make(map[string]bool, len(set))
		for id := range set {
			reach[id] = true
		}
		if adminErr == nil && h.adminBypassFor(p) {
			for _, a := range admins {
				reach[a.ID] = true
			}
		}
		out[p] = adminview.AccessSummary{
			Path:      p,
			UserCount: len(reach),
			TotalUser: total,
		}
	}
	return out
}

// withAdminBypass appends the admins who walk past this path's tags, each
// labelled with the rule that lets them. Users already listed keep their
// place and just gain the extra reason.
func (h *Handler) withAdminBypass(ctx context.Context, path string, users []adminUser) []adminUser {
	if !h.adminBypassFor(path) {
		return users
	}
	admins, err := h.repo.AdminUsers(ctx)
	if err != nil {
		return users
	}
	reason := h.adminReason(path)
	seen := make(map[string]int, len(users))
	for i, u := range users {
		seen[u.ID] = i
	}
	for _, a := range admins {
		if i, ok := seen[a.ID]; ok {
			if !containsString(users[i].ViaTags, reason) {
				users[i].ViaTags = append(users[i].ViaTags, reason)
			}
			continue
		}
		a.ViaTags = []string{reason}
		users = append(users, a)
	}
	return users
}

// accessUsersPage serves GET /admin/access/users?path=… — the modal behind the
// reach number. Returns JSON; the page renders it client-side (the admin panel
// has no htmx, see layout.templ).
func (h *Handler) accessUsersPage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" || !strings.HasPrefix(path, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	// Connected accounts do not follow the plain tag rule — see
	// accountAccessUsers — so they get their own resolution.
	if strings.HasPrefix(path, connectors.AccountTagPath("")) {
		detail, err := h.accountAccessDetail(r.Context(), path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}
	detail, err := h.repo.AccessDetail(r.Context(), path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	detail.Path = path
	detail.Kind = accessKindOf(path)
	detail.KindOne = accessKindOneOf(path)
	if !detail.Public {
		// A public item already lists everyone, admins included.
		detail.Users = h.withAdminBypass(r.Context(), path, detail.Users)
	}
	writeJSON(w, http.StatusOK, detail)
}

// tagUsagePage serves GET /admin/tags/{id}/usage — the "!" detail behind a
// tag's counters: which users carry it, and what it grants access to.
func (h *Handler) tagUsagePage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tag id is required"})
		return
	}
	usage, err := h.repo.TagUsageDetail(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Group the granted items by surface so the modal reads as
	// "Connectors (3) · Jobs (1)" rather than a flat list of raw paths.
	byKind := map[string][]AccessItem{}
	for _, it := range usage.Items {
		it.Kind = accessKindOf(it.Path)
		byKind[it.Kind] = append(byKind[it.Kind], it)
	}
	kinds := make([]AccessItemGroup, 0, len(byKind))
	for kind, items := range byKind {
		sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
		kinds = append(kinds, AccessItemGroup{Kind: kind, Items: items})
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	usage.Groups = kinds
	usage.Items = nil
	writeJSON(w, http.StatusOK, usage)
}

// adminUser is the projection the access modals render — enough to recognise
// a person, nothing more. Email is already visible on /admin/users.
type adminUser struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Email    string   `json:"email"`
	Role     string   `json:"role"`
	Approved bool     `json:"approved"`
	ViaTags  []string `json:"via_tags"`
}

// AccessDetail answers "who reaches this one item, and how".
type AccessDetail struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	// KindOne is Kind in the singular, for the modal's sentence.
	KindOne string `json:"kind_one"`
	// Public: the item carries no filter tag, so every approved user reaches
	// it and Users lists all of them.
	Public bool        `json:"public"`
	Tags   []string    `json:"tags"`
	Users  []adminUser `json:"users"`
}

// AccessItem is one thing a tag grants access to.
type AccessItem struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// AccessItemGroup buckets a tag's granted items by surface.
type AccessItemGroup struct {
	Kind  string       `json:"kind"`
	Items []AccessItem `json:"items"`
}

// TagUsage is the counters shown on the Tags page plus the detail behind them.
type TagUsage struct {
	TagID     string            `json:"tag_id"`
	TagName   string            `json:"tag_name"`
	UserCount int               `json:"user_count"`
	ItemCount int               `json:"item_count"`
	Users     []adminUser       `json:"users,omitempty"`
	Items     []AccessItem      `json:"items,omitempty"`
	Groups    []AccessItemGroup `json:"groups,omitempty"`
}

// decorateResourceRows fills the reach badge and the search blob on a
// ResourcesAdminPage listing (Projects / Workflows / Skills / Data Tables).
// One call per page keeps the four handlers from each growing their own copy
// of the same two lookups.
func (h *Handler) decorateResourceRows(ctx context.Context, rows []adminview.ResourceAdminRow, allTags []*entity.Tag) []adminview.ResourceAdminRow {
	paths := make([]string, 0, len(rows))
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	access := h.accessSummaries(ctx, paths)
	for i := range rows {
		rows[i].Access = access[rows[i].Path]
		rows[i].TagNames = adminview.TagNames(allTags, rows[i].TagIDs)
	}
	return rows
}

// ── Connected accounts ────────────────────────────────────────────────────
//
// An account's reach is NOT just "who carries its tags". connectors.
// AccountVisibleTo also lets in the person who connected it, the instance
// owner, admins (while admin_see_all_connectors is on) and — when the
// instance opted into AllowOthersSeeAccounts — everybody who can see the row
// at all. Counting only the tags would understate exactly the case that
// matters: who can post as this Slack identity.

// accountReasons labels why a user reaches an account, shown as the "via"
// chips in the modal.
const (
	reasonConnected = "connected it"
	reasonOwner     = "instance owner"
	reasonPool      = "pool shared"
)

// accountAccessUsers resolves the full set of people who can see — and
// therefore run as — one connected account, each with the reason(s) they get
// in. Order is stable: tag holders first, then the implicit grants.
func (h *Handler) accountAccessUsers(ctx context.Context, row entity.Connector, acc entity.ConnectorAccount) ([]adminUser, error) {
	merged := map[string]*adminUser{}
	order := []string{}
	add := func(u adminUser, reason string) {
		cur, ok := merged[u.ID]
		if !ok {
			if reason != "" {
				u.ViaTags = append(u.ViaTags, reason)
			}
			cp := u
			merged[u.ID] = &cp
			order = append(order, u.ID)
			return
		}
		if reason != "" && !containsString(cur.ViaTags, reason) {
			cur.ViaTags = append(cur.ViaTags, reason)
		}
	}

	// 1. Tagged share: an admin handed this one account to a team.
	detail, err := h.repo.AccessDetail(ctx, connectors.AccountTagPath(acc.ID))
	if err != nil {
		return nil, err
	}
	if !detail.Public { // Public here just means "no tags on the account".
		for _, u := range detail.Users {
			add(u, "")
		}
	}

	// 2. Whole pool shared by the instance's access policy.
	if row.AllowOthersSeeAccounts {
		rowDetail, err := h.repo.AccessDetail(ctx, "/connectors/"+row.ID)
		if err != nil {
			return nil, err
		}
		for _, u := range rowDetail.Users {
			add(u, reasonPool)
		}
	}

	// 3. The person who connected it, and the person who created the row.
	for _, pair := range []struct{ id, reason string }{
		{acc.WickUserID, reasonConnected},
		{row.CreatedBy, reasonOwner},
	} {
		if pair.id == "" {
			continue
		}
		u, err := h.repo.AdminUserByID(ctx, pair.id)
		if err != nil || u == nil {
			continue
		}
		add(*u, pair.reason)
	}

	// 4. Admins, but only while the knob that grants them the bypass is on —
	//    with it off an admin is scoped like anyone else here. Same rule and
	//    same wording as every other surface (adminBypassFor).
	if h.adminBypassFor(connectors.AccountTagPath(acc.ID)) {
		admins, err := h.repo.AdminUsers(ctx)
		if err == nil {
			reason := h.adminReason(connectors.AccountTagPath(acc.ID))
			for _, u := range admins {
				add(u, reason)
			}
		}
	}

	out := make([]adminUser, 0, len(order))
	for _, id := range order {
		out = append(out, *merged[id])
	}
	return out, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// accountAccessSummary turns that set into the badge. An account is never
// "public" — the floor is the person who connected it — so it always renders
// as a restricted count.
func (h *Handler) accountAccessSummary(ctx context.Context, row entity.Connector, acc entity.ConnectorAccount, total int) adminview.AccessSummary {
	users, err := h.accountAccessUsers(ctx, row, acc)
	if err != nil {
		return adminview.AccessSummary{Path: connectors.AccountTagPath(acc.ID), Unknown: true}
	}
	return adminview.AccessSummary{
		Path:      connectors.AccountTagPath(acc.ID),
		UserCount: len(users),
		TotalUser: total,
	}
}

// accountAccessDetail is the modal body for a connected account: the full set
// from accountAccessUsers, tagged with the reason each person gets in.
func (h *Handler) accountAccessDetail(ctx context.Context, path string) (AccessDetail, error) {
	out := AccessDetail{Path: path, Kind: accessKindOf(path), KindOne: accessKindOneOf(path)}
	accID := strings.TrimPrefix(path, connectors.AccountTagPath(""))
	if h.connectors == nil || accID == "" {
		return out, errNoAccount
	}
	acc, err := h.connectors.GetAccount(ctx, accID)
	if err != nil || acc == nil {
		return out, errNoAccount
	}
	row, err := h.connectors.Get(ctx, acc.ConnectorID)
	if err != nil || row == nil {
		return out, errNoAccount
	}
	tagDetail, err := h.repo.AccessDetail(ctx, path)
	if err != nil {
		return out, err
	}
	if !tagDetail.Public {
		out.Tags = tagDetail.Tags
	}
	users, err := h.accountAccessUsers(ctx, *row, *acc)
	if err != nil {
		return out, err
	}
	out.Users = users
	return out, nil
}

// errNoAccount keeps the modal honest when an account id no longer resolves —
// better a clear error than an empty list that reads as "nobody".
var errNoAccount = errors.New("connected account not found")

// ── The admin bypass ──────────────────────────────────────────────────────
//
// "Who can reach this" is never just the tag holders: on most surfaces the
// admin ROLE walks past the tags. How far it walks differs per surface, and
// two of them are behind knobs, so the reach has to be the UNION of the tag
// holders with whichever admins currently bypass — not the tag count alone.
//
//	/tools, /jobs, /manager  — login.CanAccessTool returns true for any admin,
//	                           unconditionally. No knob.
//	/providers               — providerPerm grants admins access and manage,
//	                           unconditionally. No knob.
//	/connectors, accounts    — adminscope.AdminSeeAllConnectors (default ON).
//	/projects, /data-tables,
//	/workflows, /skills      — adminscope.AdminSeeAllSessions (default OFF).
//
// An admin who ALSO carries a matching tag must be counted once, so callers
// union user ids rather than adding two numbers.
func (h *Handler) adminBypassFor(path string) bool {
	switch strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0] {
	case "tools", "jobs", "manager", "providers":
		return true
	case "connectors", "connector-accounts":
		if h.connectors != nil {
			return h.connectors.AdminSeesAllConnectors()
		}
		return adminscope.AdminSeeAllConnectors(h.configs)
	case "projects", "data-tables", "workflows", "skills":
		return adminscope.AdminSeeAllSessions(h.configs)
	default:
		return false
	}
}

// adminReason names why an admin is in a reach list, naming the knob when one
// is involved — "admin" alone invites the question this string answers.
func (h *Handler) adminReason(path string) string {
	switch strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0] {
	case "connectors", "connector-accounts":
		return "admin (see-all connectors on)"
	case "projects", "data-tables", "workflows", "skills":
		return "admin (see-all sessions on)"
	default:
		return "admin role"
	}
}
