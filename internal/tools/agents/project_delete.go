package agents

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/claude"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/pkg/tool"
)

// workspaceForgetters drops what a provider CLI keeps on its own disk for one
// cwd, keyed by provider type and called with the instance's config dir. Only
// providers whose storage is keyed by cwd belong here:
//
//   - claude   — `<config dir>/projects/<encoded cwd>/`: transcripts + memory.
//   - codex    — rollouts are filed by date (`sessions/YYYY/MM/DD/`), not cwd.
//   - opencode — sessions live in its own database, not a per-cwd folder.
//   - gemini, omp — not wired: their per-project folder naming is not
//     something wick spawns or verifies, and removing a guessed path could
//     hit another project's history.
var workspaceForgetters = map[provider.Type]func(configDir, cwd string) error{
	provider.TypeClaude: claude.ForgetWorkspace,
}

// errProtectedProject refuses the default/personal project.
var errProtectedProject = errors.New("this project is protected and cannot be deleted")

// projectDeletePreview is what the delete dialog shows before the user
// confirms: how many chats go, and whether the folder goes with them.
type projectDeletePreview struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Chats      int    `json:"chats"`
	CustomPath string `json:"custom_path,omitempty"`
	Protected  bool   `json:"protected"`
	// Channels names the channel conversations in the project
	// ("slack · C0123"), Schedules counts the live schedules firing into
	// it — what stops if the project goes, and what keeps running (now as
	// the agent) if it is converted.
	Channels  []string `json:"channels"`
	Schedules int64    `json:"schedules"`
	// Workflows names the workflows with a node bound to this project —
	// they keep sending into it after a convert, and lose their target
	// after a delete.
	Workflows []string `json:"workflows"`
}

func previewProjectDelete(ctx context.Context, p project.Project) projectDeletePreview {
	sids := globalMgr.ProjectSessionIDs(p.Meta.ID)
	pv := projectDeletePreview{
		ID:         p.Meta.ID,
		Name:       p.Meta.Name,
		Chats:      len(sids),
		CustomPath: p.Meta.CustomPath,
		Protected:  project.IsProtected(p.Meta),
		Channels:   projectChannels(sids),
	}
	if globalSchedule != nil {
		pv.Schedules, _ = globalSchedule.CountTargeting(ctx, p.Meta.ID, sids)
	}
	pv.Workflows = projectWorkflows(p.Meta.ID)
	return pv
}

// projectWorkflows lists the names of the workflows that bind a node to
// projectID. A workflow that fails to load is skipped: the preview is a
// heads-up, not a gate.
func projectWorkflows(projectID string) []string {
	if globalWorkflowMgr == nil || globalWorkflowMgr.Service == nil {
		return []string{}
	}
	ids, err := globalWorkflowMgr.Service.List()
	if err != nil {
		return []string{}
	}
	var wfs []workflow.Workflow
	for _, id := range ids {
		if w, err := globalWorkflowMgr.Service.Load(id); err == nil {
			wfs = append(wfs, w)
		}
	}
	return workflowsBoundTo(wfs, projectID)
}

// workflowsBoundTo returns, sorted, the names (id when unnamed) of the
// workflows with a node whose project binding is projectID.
func workflowsBoundTo(wfs []workflow.Workflow, projectID string) []string {
	out := []string{}
	if projectID == "" {
		return out
	}
	for _, w := range wfs {
		for _, n := range w.Graph.Nodes {
			if n.Workspace != projectID {
				continue
			}
			name := w.Name
			if name == "" {
				name = w.ID
			}
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
			break
		}
	}
	slices.Sort(out)
	return out
}

// projectChannels lists the distinct channels (origin + channel id) the
// top-level sessions of sids came from; web, API and schedule chats are
// not channels.
func projectChannels(sids []string) []string {
	out := []string{}
	for _, sid := range sids {
		s, ok := globalMgr.Registry().Session(sid)
		if !ok || s.Meta.ParentSessionID != "" {
			continue
		}
		switch s.Meta.Origin {
		case session.OriginUI, session.OriginREST, session.OriginSchedule, "":
			continue
		}
		label := string(s.Meta.Origin)
		if s.Meta.ChannelID != "" {
			label += " · " + s.Meta.ChannelID
		}
		if !slices.Contains(out, label) {
			out = append(out, label)
		}
	}
	slices.Sort(out)
	return out
}

// projectDeletePreviewJSON handles GET /projects/{id}/delete-preview.
// Access is enforced by projectAccessMW.
func projectDeletePreviewJSON(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	p, ok := globalMgr.Registry().Project(c.PathValue("id"))
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	c.JSON(http.StatusOK, previewProjectDelete(c.Context(), p))
}

// purgeProject deletes a project for good: it stops the agents running in
// its sessions, cancels the schedules that would fire into it, removes the
// sessions (sub-agents included) and the project folder, then forgets the
// provider history kept for its managed cwd. A custom path is the user's
// folder: it stays, and so does the provider history of it.
func purgeProject(ctx context.Context, p project.Project, by string) error {
	id := p.Meta.ID
	if project.IsProtected(p.Meta) {
		return errProtectedProject
	}
	sids := globalMgr.ProjectSessionIDs(id)
	if globalPool != nil {
		for _, sid := range sids {
			if err := globalPool.KillBy(sid, "", by, "project deleted"); err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("session", sid).Msg("delete project: stop agent")
			}
		}
	}
	if globalSchedule != nil {
		if n, err := globalSchedule.CancelTargeting(ctx, id, sids); err != nil {
			log.Ctx(ctx).Warn().Err(err).Str("project", id).Msg("delete project: cancel schedules")
		} else if n > 0 {
			log.Ctx(ctx).Info().Int64("schedules", n).Str("project", id).Msg("delete project: schedules cancelled")
		}
	}
	managedCwd := ""
	if p.Meta.CustomPath == "" {
		managedCwd = globalLayout.ProjectManagedPath(id)
	}
	if err := globalMgr.DeleteProject(ctx, id); err != nil {
		return err
	}
	if globalTagsSvc != nil {
		_ = globalTagsSvc.DeleteResourceOwnerTag(ctx, id)
	}
	if managedCwd != "" {
		forgetWorkspaceFn(ctx, managedCwd)
	}
	log.Ctx(ctx).Info().Str("project", id).Int("sessions", len(sids)).Msg("project deleted")
	return nil
}

// forgetWorkspaceFn is forgetWorkspace, swappable so tests never reach the
// provider config dirs of the machine running them.
var forgetWorkspaceFn = forgetWorkspace

// forgetWorkspace runs every workspaceForgetter once per distinct config
// dir across the configured instances. Best effort: the project is already
// gone, so a leftover history folder is logged, not returned.
func forgetWorkspace(ctx context.Context, cwd string) {
	instances, err := provider.Load()
	if err != nil {
		log.Ctx(ctx).Warn().Err(err).Msg("delete project: load provider instances")
		return
	}
	var seen []string
	for _, ins := range instances {
		forget, ok := workspaceForgetters[ins.Type]
		if !ok {
			continue
		}
		dir := logintty.ConfigDir(ins.Type, ins.Env)
		key := string(ins.Type) + "\x00" + dir
		if dir == "" || slices.Contains(seen, key) {
			continue
		}
		seen = append(seen, key)
		if err := forget(dir, cwd); err != nil {
			log.Ctx(ctx).Warn().Err(err).Str("provider", ins.Name).Str("cwd", cwd).Msg("delete project: forget workspace")
		}
	}
}
