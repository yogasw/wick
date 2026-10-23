package agents

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/trash"
	"github.com/yogasw/wick/pkg/tool"
)

// Deleted projects wait in the trash (internal/agents/trash) for
// trash.Retention. The routes below list, restore and purge them; the
// sweeper removes the ones past the window.

// trashSweepEvery is how often the retention sweep looks at the trash.
// Hourly is plenty for a 30-day window and costs one ReadDir.
const trashSweepEvery = time.Hour

// trashItem is one Recently deleted row.
type trashItem struct {
	Key           string    `json:"key"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	OwnerUserID   string    `json:"owner_user_id,omitempty"`
	DeletedBy     string    `json:"deleted_by,omitempty"`
	DeletedByName string    `json:"deleted_by_name,omitempty"`
	DeletedAt     time.Time `json:"deleted_at"`
	Chats         int       `json:"chats"`
	Schedules     int       `json:"schedules"`
	CustomPath    string    `json:"custom_path,omitempty"`
	DaysLeft      int       `json:"days_left"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// trashRestoreResult tells the caller what came back and what did not.
type trashRestoreResult struct {
	ProjectID string `json:"project_id"`
	Sessions  int    `json:"sessions"`
	// MissingSessions were in the manifest but no longer load.
	MissingSessions []string `json:"missing_sessions,omitempty"`
	// SchedulesNotRestored were cancelled by the delete and stay
	// cancelled: re-arming a timer that missed its runs is a human call.
	SchedulesNotRestored []trash.Schedule `json:"schedules_not_restored"`
}

// canSeeTrash: the project's owner, or an admin.
func canSeeTrash(m trash.Manifest, uid string, admin bool) bool {
	return admin || (uid != "" && m.OwnerUserID == uid)
}

func toTrashItem(m trash.Manifest, now time.Time) trashItem {
	exp := m.DeletedAt.Add(trash.Retention)
	days := int(exp.Sub(now).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return trashItem{
		Key: m.Key, ID: m.ID, Name: m.Name, OwnerUserID: m.OwnerUserID, DeletedBy: m.DeletedBy,
		DeletedAt: m.DeletedAt, Chats: len(m.SessionIDs), Schedules: len(m.Schedules),
		CustomPath: m.CustomPath, DaysLeft: days, ExpiresAt: exp,
	}
}

// projectTrashJSON handles GET /projects-trash: the entries the caller
// may restore, newest first.
func projectTrashJSON(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	all, err := trash.List(globalLayout.BaseDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	uid, admin := actorID(c), callerIsAdmin(c)
	now := time.Now()
	items := []trashItem{}
	ids := map[string]bool{}
	for _, m := range all {
		if !canSeeTrash(m, uid, admin) {
			continue
		}
		items = append(items, toTrashItem(m, now))
		ids[m.DeletedBy] = true
	}
	names := userNames(c, ids)
	for i := range items {
		items[i].DeletedByName = names[items[i].DeletedBy]
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "retention_days": int(trash.Retention.Hours() / 24)})
}

// loadTrashEntry reads {key} and answers 404 when it is missing or the
// caller may not see it (an entry you may not restore does not exist for
// you).
func loadTrashEntry(c *tool.Ctx) (trash.Manifest, bool) {
	m, err := trash.Load(globalLayout.BaseDir, c.PathValue("key"))
	if err == nil && canSeeTrash(m, actorID(c), callerIsAdmin(c)) {
		return m, true
	}
	if err != nil && !errors.Is(err, trash.ErrNotFound) {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return m, false
	}
	c.JSON(http.StatusNotFound, map[string]string{"error": "deleted project not found"})
	return m, false
}

// projectTrashRestore handles POST /projects-trash/{key}/restore.
func projectTrashRestore(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	m, ok := loadTrashEntry(c)
	if !ok {
		return
	}
	res, err := restoreProject(c.Context(), m)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errTrashCollision) || errors.Is(err, trash.ErrOccupied) {
			status = http.StatusConflict
		}
		c.JSON(status, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// projectTrashPurge handles DELETE /projects-trash/{key}: delete forever,
// admins only.
func projectTrashPurge(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !callerIsAdmin(c) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "only an admin can delete a project forever"})
		return
	}
	m, ok := loadTrashEntry(c)
	if !ok {
		return
	}
	if _, err := trash.Purge(globalLayout.BaseDir, m.Key); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Ctx(c.Context()).Info().Str("project", m.ID).Str("trash", m.Key).Str("by", actorID(c)).Msg("deleted project purged by hand")
	c.JSON(http.StatusOK, map[string]string{"status": "purged"})
}

var errTrashCollision = errors.New("a project with this id already exists")

// restoreProject moves an entry back and adopts it into the registry.
// Schedules are reported, not re-armed.
func restoreProject(ctx context.Context, m trash.Manifest) (trashRestoreResult, error) {
	if _, ok := globalMgr.Registry().Project(m.ID); ok {
		return trashRestoreResult{}, errTrashCollision
	}
	if _, err := trash.Restore(globalLayout.BaseDir, m.Key); err != nil {
		return trashRestoreResult{}, err
	}
	_, missing, err := globalMgr.AdoptRestored(m.ID, m.SessionIDs)
	if err != nil {
		return trashRestoreResult{}, err
	}
	log.Ctx(ctx).Info().Str("project", m.ID).Str("trash", m.Key).Str("by", actorOf(ctx)).
		Int("sessions", len(m.SessionIDs)-len(missing)).Int("schedules_not_restored", len(m.Schedules)).
		Msg("project restored from trash")
	return trashRestoreResult{
		ProjectID: m.ID, Sessions: len(m.SessionIDs) - len(missing),
		MissingSessions: missing, SchedulesNotRestored: m.Schedules,
	}, nil
}

// sweepProjectTrash purges the entries past trash.Retention, logging each.
func sweepProjectTrash(ctx context.Context, now time.Time) []trash.Manifest {
	if globalLayout.BaseDir == "" {
		return nil
	}
	gone, err := trash.PurgeOlderThan(globalLayout.BaseDir, now, trash.Retention)
	if err != nil {
		log.Ctx(ctx).Warn().Err(err).Msg("project trash: purge")
	}
	for _, m := range gone {
		log.Ctx(ctx).Info().Str("project", m.ID).Str("name", m.Name).Str("trash", m.Key).
			Time("deleted_at", m.DeletedAt).Int("sessions", len(m.SessionIDs)).
			Msg("project trash: purged past retention")
	}
	return gone
}

var startProjectTrashSweep sync.Once

// startProjectTrashSweeper runs the retention sweep now and every
// trashSweepEvery, for the life of the process.
func startProjectTrashSweeper() {
	startProjectTrashSweep.Do(func() {
		go func() {
			ctx := context.Background()
			t := time.NewTicker(trashSweepEvery)
			defer t.Stop()
			for {
				sweepProjectTrash(ctx, time.Now())
				<-t.C
			}
		}()
	})
}
