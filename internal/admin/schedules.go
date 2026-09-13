package admin

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	adminview "github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// ── Schedules ────────────────────────────────────────────────────────────────
//
// The admin view of scheduled messages, and the one place the RUN-AS identity
// can be changed.
//
// Why this page exists at all: a schedule runs as a user, and everything it
// can reach follows from that. When nobody is attached the fire falls back to
// wick's synthetic internal principal — admin-role, but carrying no access
// tags — so the job quietly sees far less than the person who set it up. That
// failure is invisible from every other screen, because the scheduled monitor
// reports what a schedule DOES and not whom it does it as. Here the identity
// is the subject: one row per schedule, whose it is, and what it will actually
// run as on the next fire.
//
// Run-as is admin-only on purpose. It decides whose access a fire borrows, so
// a schedule's own owner must never be able to set it — that would turn every
// schedule into a way to inherit somebody else's reach.

// ScheduleLister is the slice of the schedule store this page needs, kept as
// an interface so package admin does not depend on the store's concrete type.
type ScheduleLister interface {
	ListAll(ctx context.Context, limit int) ([]entity.ScheduledMessage, error)
	Reschedule(ctx context.Context, id string, patch schedule.SchedulePatch) error
}

// ProjectNamer resolves a project id to its display name, so the page can show
// "Ygsw Bot" instead of a uuid.
type ProjectNamer interface {
	ProjectName(id string) string
}

// SetSchedules wires the schedules page post-construction, matching how the
// other optional dependencies are attached. Nil simply leaves the page
// reporting that scheduling is not configured on this instance.
func (h *Handler) SetSchedules(l ScheduleLister, p ProjectNamer) {
	h.schedules = l
	h.projectNames = p
}

// adminSchedulesLimit caps the listing. Schedules are counted in dozens, not
// thousands; a cap keeps one runaway install from rendering forever.
const adminSchedulesLimit = 2000

// adminSchedulesPageSize keeps one screen readable. A host that has been
// running for months accumulates hundreds of finished one-shots, and the rows
// that still matter are a handful — paging is what keeps those visible.
//
// Five, not a screenful: each row carries a Run as control and a two-line
// caption, so a page of them is already tall, and this screen is for acting on
// a few schedules rather than skimming many.
const adminSchedulesPageSize = 5

// scheduleIsLive reports whether a schedule can still fire. "paused" counts:
// it keeps its place in the cadence and resuming is one click.
func scheduleIsLive(status string) bool {
	return status == entity.ScheduledStatusPending ||
		status == entity.ScheduledStatusActive ||
		status == "paused"
}

// scheduleMatchesQuery does a case-insensitive contains over the fields
// somebody would actually search by: what the job says, its id, the project
// it runs in, and who it runs as.
func scheduleMatchesQuery(row adminview.ScheduleAdminRow, q string) bool {
	if q == "" {
		return true
	}
	for _, field := range []string{row.Message, row.ID, row.ProjectName, row.ProjectID, row.OwnerLabel, row.RunAsLabel, row.SessionID} {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	return false
}

func (h *Handler) schedulesAdminPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := login.GetUser(ctx)
	vm := adminview.ScheduleAdminVM{
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
		Query:      strings.TrimSpace(r.URL.Query().Get("q")),
		Configured: h.schedules != nil,
		PageSize:   adminSchedulesPageSize,
	}
	// Default view is LIVE. A finished schedule cannot run as the wrong user
	// ever again, so showing it by default buries the ones that can.
	if vm.Status == "" {
		vm.Status = "live"
	}
	if !vm.Configured {
		adminview.SchedulesAdminPage(vm, user).Render(ctx, w)
		return
	}

	rows, err := h.schedules.ListAll(ctx, adminSchedulesLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// One name lookup for the whole page, not one per row.
	names := h.userLabels(ctx)
	all := make([]adminview.ScheduleAdminRow, 0, len(rows))
	for _, m := range rows {
		row := adminview.ScheduleAdminRow{
			ID:          m.ID,
			Message:     m.Message,
			Kind:        m.Kind,
			Status:      m.EffectiveStatus(),
			Cron:        m.Cron,
			SessionID:   m.SessionID,
			SessionMode: m.Mode(),
			ProjectID:   m.ProjectID,
			CreatedBy:   m.CreatedBy,
			OwnerUserID: m.OwnerUserID,
			RunAsUserID: m.RunAsUserID,
			RunAs:       m.EffectiveRunAsUser(),
		}
		if m.ProjectID != "" && h.projectNames != nil {
			row.ProjectName = h.projectNames.ProjectName(m.ProjectID)
		}
		if next := m.NextRunAt(); next != nil {
			row.NextRunAt = next.UTC().Format(time.RFC3339)
			row.NextRunLabel = next.UTC().Format("02 Jan 15:04")
		}
		row.OwnerLabel = labelFor(names, m.OwnerUserID)
		row.RunAsLabel = labelFor(names, row.RunAs)
		all = append(all, row)
	}

	// Counts are computed over everything, so a tab still shows how much is
	// behind it while you are looking at another one.
	vm.Counts = map[string]int{"all": len(all)}
	for _, row := range all {
		if scheduleIsLive(row.Status) {
			vm.Counts["live"]++
			continue
		}
		vm.Counts[row.Status]++
	}

	q := strings.ToLower(vm.Query)
	filtered := make([]adminview.ScheduleAdminRow, 0, len(all))
	for _, row := range all {
		switch vm.Status {
		case "all":
		case "live":
			if !scheduleIsLive(row.Status) {
				continue
			}
		default:
			if row.Status != vm.Status {
				continue
			}
		}
		if !scheduleMatchesQuery(row, q) {
			continue
		}
		filtered = append(filtered, row)
	}

	// Unattached first inside the live view — those are the ones this page
	// exists to catch — then by next fire, so it reads as "what happens next".
	sort.SliceStable(filtered, func(i, j int) bool {
		if scheduleIsLive(filtered[i].Status) != scheduleIsLive(filtered[j].Status) {
			return scheduleIsLive(filtered[i].Status)
		}
		if (filtered[i].RunAs == "") != (filtered[j].RunAs == "") {
			return filtered[i].RunAs == ""
		}
		return filtered[i].NextRunAt < filtered[j].NextRunAt
	})

	vm.Total = len(filtered)
	vm.Pages = (vm.Total + adminSchedulesPageSize - 1) / adminSchedulesPageSize
	vm.Page = 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 1 {
		vm.Page = p
	}
	if vm.Pages > 0 && vm.Page > vm.Pages {
		vm.Page = vm.Pages
	}
	start := (vm.Page - 1) * adminSchedulesPageSize
	if start > vm.Total {
		start = vm.Total
	}
	end := start + adminSchedulesPageSize
	if end > vm.Total {
		end = vm.Total
	}
	vm.Rows = filtered[start:end]
	vm.Users = h.userOptions(ctx)

	adminview.SchedulesAdminPage(vm, user).Render(ctx, w)
}

// setScheduleRunAs points a schedule at a user — or clears the override with
// an empty value, handing the schedule back to its owner.
func (h *Handler) setScheduleRunAs(w http.ResponseWriter, r *http.Request) {
	if h.schedules == nil {
		http.Error(w, "scheduling is not configured", http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unreadable form", http.StatusBadRequest)
		return
	}
	runAs := r.FormValue("run_as_user_id")
	// The internal principal is not a real account and holds no tags. Naming
	// it would be asking for exactly the ownerless behaviour this page exists
	// to surface, so it is refused rather than stored.
	if runAs == internalAgentUserID {
		http.Error(w, "cannot run as the internal agent principal", http.StatusBadRequest)
		return
	}
	if err := h.schedules.Reschedule(r.Context(), id, schedule.SchedulePatch{RunAsUserID: &runAs}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectOrNoContent(w, r, "/admin/schedule")
}

// internalAgentUserID mirrors the MCP auth package's synthetic principal id.
// Duplicated rather than imported to keep package admin free of a dependency
// on the MCP server for one constant.
const internalAgentUserID = "wick-agent-internal"

// userLabels maps user id → display name for the rows.
func (h *Handler) userLabels(ctx context.Context) map[string]string {
	out := map[string]string{}
	if h.repo == nil {
		return out
	}
	users, err := h.repo.ListUsers(ctx)
	if err != nil {
		return out
	}
	for _, u := range users {
		out[u.ID] = displayName(u)
	}
	return out
}

// userOptions is the "Run as" picker's contents: approved users only. An
// unapproved account would be accepted here and then refused at fire time,
// which is a worse way to find out.
func (h *Handler) userOptions(ctx context.Context) []adminview.ScheduleUserOption {
	out := []adminview.ScheduleUserOption{}
	if h.repo == nil {
		return out
	}
	users, err := h.repo.ListUsers(ctx)
	if err != nil {
		return out
	}
	for _, u := range users {
		if !u.Approved {
			continue
		}
		out = append(out, adminview.ScheduleUserOption{ID: u.ID, Label: displayName(u)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func displayName(u *entity.User) string {
	switch {
	case u.Name != "":
		return u.Name
	case u.Email != "":
		return u.Email
	default:
		return u.ID
	}
}

// labelFor renders an id as a name, falling back to the id so an account that
// has since been deleted is still identifiable rather than blank.
func labelFor(names map[string]string, id string) string {
	if id == "" {
		return ""
	}
	if n, ok := names[id]; ok {
		return n
	}
	return id
}
