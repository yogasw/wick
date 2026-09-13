package admin

import (
	"context"
	"net/http"

	agentproject "github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/schedule"
)

// ── Ownership ────────────────────────────────────────────────────────────────
//
// Who a connector instance, a project or a schedule belongs to, and the one
// place that can be changed.
//
// Ownership is not a label. On a connector the owner is the non-admin who may
// configure the instance and who sees every OAuth account connected to it; on
// a project the owner keeps access to it regardless of tags; on a schedule the
// owner is whose identity a fire borrows when no run-as override is set. All
// three are stamped once, at create time, from whoever happened to be holding
// the mouse — and nothing could restamp them afterwards. When that person
// leaves, the row is left administered by an account nobody can sign into, and
// the only remaining route is an admin editing the database by hand.
//
// Admin-only on purpose, and for the same reason run-as is: letting an owner
// hand ownership on would let them hand on the reach that comes with it.

// ProjectWriter persists a project's metadata. Satisfied by
// registry.Manager — kept as an interface so package admin keeps depending on
// the registry only through the shapes it actually uses.
type ProjectWriter interface {
	UpdateProject(ctx context.Context, id string, meta agentproject.Meta) (agentproject.Project, error)
}

// SetProjectWriter wires the writer behind the projects Owner picker. Optional,
// like SetDataTables and SetSchedules: nil leaves the page rendering and the
// endpoint refusing, rather than panicking.
func (h *Handler) SetProjectWriter(w ProjectWriter) { h.projectWriter = w }

// ownerFromForm reads and validates the requested owner.
//
// The empty string is a legitimate answer — "no owner", which is the state
// most seeded rows are already in — so it is allowed through. Anything else
// has to name a real, approved user: the picker only offers those, but the
// endpoint is reachable without it, and an id naming nobody would silently
// leave the row administered by an account that cannot be signed into. The
// internal agent principal is refused for the same reason run-as refuses it —
// it is synthetic, carries no tags, and owning something as nobody is exactly
// the failure this page exists to fix.
//
// Returns ok=false once it has written the response.
func (h *Handler) ownerFromForm(w http.ResponseWriter, r *http.Request) (string, bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unreadable form", http.StatusBadRequest)
		return "", false
	}
	owner := r.FormValue("owner_user_id")
	if owner == "" {
		return "", true
	}
	if owner == internalAgentUserID {
		http.Error(w, "the internal agent principal cannot own anything", http.StatusBadRequest)
		return "", false
	}
	u, err := h.repo.GetUser(r.Context(), owner)
	if err != nil || u == nil {
		http.Error(w, "no such user", http.StatusBadRequest)
		return "", false
	}
	if !u.Approved {
		http.Error(w, "cannot hand ownership to an unapproved user", http.StatusBadRequest)
		return "", false
	}
	return owner, true
}

// setConnectorOwner re-stamps a connector instance's owner.
//
// Two writes, in this order: the row's CreatedBy (what connectors.OwnsConnector
// reads, and therefore what actually decides who may configure the instance and
// see its account pool), then the "owner:{id}" tag that carries the configure
// grant. The tag alone would not move ownership, and the column alone would
// leave the previous owner still carrying a grant to a row they no longer own —
// which is the exact leak the OAuth-callback fix closed from the other side.
func (h *Handler) setConnectorOwner(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	owner, ok := h.ownerFromForm(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	row, err := h.connectors.Get(ctx, id)
	if err != nil || row == nil {
		http.Error(w, "no such connector instance", http.StatusNotFound)
		return
	}
	previous := row.CreatedBy
	if previous == owner {
		redirectOrNoContent(w, r, "/admin/connectors")
		return
	}
	if err := h.connectors.SetOwner(ctx, id, owner); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The tag is what a non-admin owner's configure rights hang off, so a
	// failure here leaves the row half-transferred and must be reported, not
	// swallowed: the new owner would be recorded but unable to act.
	if err := h.repo.TransferOwnerTag(ctx, id, "/connectors/"+id, previous, owner); err != nil {
		http.Error(w, "owner set, but the owner tag could not be moved: "+err.Error(), http.StatusInternalServerError)
		return
	}
	redirectOrNoContent(w, r, "/admin/connectors")
}

// setProjectOwner re-stamps a project's owner.
//
// The owner lives in the project's meta.json (Meta.OwnerUserID), which is what
// the sessions surface unions into a user's reach. The "owner:{id}" tag is
// moved alongside it — projects get one at create time, and leaving it on the
// previous owner would keep them reaching a project that is no longer theirs.
//
// Clearing the owner is allowed and means what it says: an ownerless project is
// admin-only (a non-admin reaches one only through an explicit tag grant), NOT
// public.
func (h *Handler) setProjectOwner(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil || h.projectWriter == nil {
		http.Error(w, "projects not available", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	owner, ok := h.ownerFromForm(w, r)
	if !ok {
		return
	}
	p, found := h.projects.Projects()[id]
	if !found {
		http.Error(w, "no such project", http.StatusNotFound)
		return
	}
	previous := p.Meta.OwnerUserID
	if previous == owner {
		redirectOrNoContent(w, r, "/admin/projects")
		return
	}
	meta := p.Meta
	meta.OwnerUserID = owner
	ctx := r.Context()
	if _, err := h.projectWriter.UpdateProject(ctx, id, meta); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Projects' owner tags carry no tool-path link (CreateResourceOwnerTag
	// links the user only), so none is created here either — writing one
	// would change how the project's access reads on every other surface.
	if err := h.repo.TransferOwnerTag(ctx, id, "", previous, owner); err != nil {
		http.Error(w, "owner set, but the owner tag could not be moved: "+err.Error(), http.StatusInternalServerError)
		return
	}
	redirectOrNoContent(w, r, "/admin/projects")
}

// setScheduleOwner re-stamps whose schedule a row is.
//
// Ownership matters twice here: it is the identity a fire borrows when no
// run-as override is set, and it is what the owner's own schedule lists are
// keyed on. A schedule whose owner no longer exists therefore runs as the
// tagless internal principal AND is invisible to everyone but an admin — the
// pair of failures this page exists to surface.
func (h *Handler) setScheduleOwner(w http.ResponseWriter, r *http.Request) {
	if h.schedules == nil {
		http.Error(w, "scheduling is not configured", http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	owner, ok := h.ownerFromForm(w, r)
	if !ok {
		return
	}
	if err := h.schedules.Reschedule(r.Context(), id, schedule.SchedulePatch{OwnerUserID: &owner}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectOrNoContent(w, r, "/admin/schedule")
}
