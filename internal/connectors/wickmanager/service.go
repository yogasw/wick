package wickmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/manager"
	"github.com/yogasw/wick/internal/processctl"
	"github.com/yogasw/wick/internal/userconfig"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
)

// Deps bundles every wick-internal service the handlers need. Filled
// by app.RegisterWickManagerConnector at boot — handlers capture this
// via closure (Opsi C) so no globals are introduced.
type Deps struct {
	Configs    *configs.Service
	Connectors *connectors.Service
	Jobs       *manager.Service
	Login      *login.Service
	// Tools is the resolved set of tool.Tool entries known to this
	// process, populated alongside the manager.Handler in api/server.go.
	// Used by tool_list to enumerate visible tools.
	Tools []tool.Tool
	// AppName is the per-binary basename used to resolve userconfig
	// paths (~/.<appName>/config.json). Filled from app.BuildAppName.
	AppName string
}

type handlers struct {
	deps Deps
}

func newHandlers(deps Deps) *handlers { return &handlers{deps: deps} }

// ── masking helpers ──────────────────────────────────────────────────

// maskedConfigValue returns the value to surface to the LLM. Secret
// rows hide plaintext behind "***"; pre-encrypted wick_enc_ tokens
// pass through unchanged so the LLM can re-emit them in subsequent
// calls.
func maskedConfigValue(row entity.Config) string {
	if !row.IsSecret {
		return row.Value
	}
	if row.Value == "" {
		return ""
	}
	if strings.HasPrefix(row.Value, "wick_enc_") || strings.HasPrefix(row.Value, "wick_cenc_") {
		return row.Value
	}
	return "***"
}

func configRowOut(row entity.Config) map[string]any {
	return map[string]any{
		"key":            row.Key,
		"type":           row.Type,
		"description":    row.Description,
		"options":        row.Options,
		"is_secret":      row.IsSecret,
		"is_set":         row.Value != "",
		"is_locked":      row.Locked,
		"required":       row.Required,
		"can_regenerate": row.CanRegenerate,
		"value":          maskedConfigValue(row),
	}
}

func configRowsOut(rows []entity.Config) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, configRowOut(r))
	}
	return out
}

// ── app_* ────────────────────────────────────────────────────────────

func (h *handlers) appList(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "app_list", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	return configRowsOut(h.deps.Configs.List()), nil
}

func (h *handlers) appGetConfig(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	args := map[string]string{"key": c.Input("key")}
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "app_get_config", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	row, ok := h.findAppRow(c.Input("key"))
	if !ok {
		err = fmt.Errorf("unknown app config key %q", c.Input("key"))
		return nil, err
	}
	return configRowOut(row), nil
}

func (h *handlers) appSetConfig(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	value := c.Input("value")
	args := map[string]string{"key": key, "value": maskValueForLog(value)}
	user, err := requireAdmin(ctx)
	var before, after map[string]any
	defer func() { logOpDiff(user, "app_set_config", args, before, after, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	row, ok := h.findAppRow(key)
	if !ok {
		err = fmt.Errorf("unknown app config key %q", key)
		return nil, err
	}
	if row.Locked {
		err = errLockedRow
		return nil, err
	}
	if row.Required && strings.TrimSpace(value) == "" && !row.IsSecret {
		err = errRequiredEmpty
		return nil, err
	}
	before = configRowOut(row)
	if err = h.deps.Configs.Set(ctx, key, value); err != nil {
		return nil, err
	}
	if newRow, ok := h.findAppRow(key); ok {
		after = configRowOut(newRow)
	}
	return map[string]any{"ok": true, "key": key, "before": before, "after": after}, nil
}

func (h *handlers) appRegenerateConfig(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	args := map[string]string{"key": key}
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "app_regenerate_config", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	row, ok := h.findAppRow(key)
	if !ok {
		err = fmt.Errorf("unknown app config key %q", key)
		return nil, err
	}
	if !row.CanRegenerate {
		err = errCannotRegenerate
		return nil, err
	}
	if err = h.deps.Configs.Regenerate(ctx, key); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "key": key, "regenerated_at": time.Now().UTC()}, nil
}

func (h *handlers) findAppRow(key string) (entity.Config, bool) {
	for _, r := range h.deps.Configs.List() {
		if r.Key == key {
			return r, true
		}
	}
	return entity.Config{}, false
}

// ── job_* ────────────────────────────────────────────────────────────

func (h *handlers) jobList(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireUser(ctx)
	defer func() { logOp(user, "job_list", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	rows, err := h.deps.Jobs.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, j := range rows {
		if !h.canAccessJob(ctx, user, j.Key) {
			continue
		}
		hasCfg := len(h.deps.Configs.ListOwned(j.Key)) > 0
		out = append(out, map[string]any{
			"key":          j.Key,
			"name":         j.Name,
			"description":  j.Description,
			"icon":         j.Icon,
			"schedule":     j.Schedule,
			"enabled":      j.Enabled,
			"last_status":  string(j.LastStatus),
			"last_run_at":  j.LastRunAt,
			"total_runs":   j.TotalRuns,
			"max_runs":     j.MaxRuns,
			"has_config":   hasCfg,
		})
	}
	return out, nil
}

func (h *handlers) jobGet(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	args := map[string]string{"key": key}
	user, err := requireUser(ctx)
	defer func() { logOp(user, "job_get", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if !h.canAccessJob(ctx, user, key) {
		err = errAccessDenied
		return nil, err
	}
	j, gerr := h.deps.Jobs.GetJob(ctx, key)
	if gerr != nil {
		err = gerr
		return nil, err
	}
	cfgs := h.deps.Configs.ListOwned(j.Key)
	return map[string]any{
		"meta": map[string]any{
			"key":         j.Key,
			"name":        j.Name,
			"description": j.Description,
			"icon":        j.Icon,
			"schedule":    j.Schedule,
			"enabled":     j.Enabled,
			"last_status": string(j.LastStatus),
			"last_run_at": j.LastRunAt,
			"total_runs":  j.TotalRuns,
			"max_runs":    j.MaxRuns,
		},
		"configs": configRowsOut(cfgs),
	}, nil
}

func (h *handlers) jobSetConfig(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	configKey := c.Input("config_key")
	value := c.Input("value")
	args := map[string]string{"key": key, "config_key": configKey, "value": maskValueForLog(value)}
	user, err := requireUser(ctx)
	var before, after map[string]any
	defer func() {
		logOpDiff(user, "job_set_config", args, before, after, err, time.Since(start))
	}()
	if err != nil {
		return nil, err
	}
	if !h.canAccessJob(ctx, user, key) {
		err = errAccessDenied
		return nil, err
	}
	row, ok := h.findOwnedRow(key, configKey)
	if !ok {
		err = fmt.Errorf("unknown config %s/%s", key, configKey)
		return nil, err
	}
	if row.Locked {
		err = errLockedRow
		return nil, err
	}
	if row.Required && strings.TrimSpace(value) == "" && !row.IsSecret {
		err = errRequiredEmpty
		return nil, err
	}
	before = configRowOut(row)
	if err = h.deps.Configs.SetOwned(ctx, key, configKey, value); err != nil {
		return nil, err
	}
	if newRow, ok := h.findOwnedRow(key, configKey); ok {
		after = configRowOut(newRow)
	}
	return map[string]any{"ok": true, "key": key, "config_key": configKey, "before": before, "after": after}, nil
}

func (h *handlers) jobSetSchedule(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	schedule := c.Input("schedule")
	enabled := c.InputBool("enabled")
	maxRuns := c.InputInt("max_runs")
	args := map[string]any{"key": key, "schedule": schedule, "enabled": enabled, "max_runs": maxRuns}
	user, err := requireUser(ctx)
	defer func() { logOp(user, "job_set_schedule", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if !h.canAccessJob(ctx, user, key) {
		err = errAccessDenied
		return nil, err
	}
	if err = h.deps.Jobs.UpdateSchedule(ctx, key, schedule, enabled, maxRuns); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok": true, "key": key, "schedule": schedule, "enabled": enabled, "max_runs": maxRuns,
	}, nil
}

func (h *handlers) jobRunNow(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	args := map[string]string{"key": key}
	user, err := requireUser(ctx)
	defer func() { logOp(user, "job_run_now", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if !h.canAccessJob(ctx, user, key) {
		err = errAccessDenied
		return nil, err
	}
	runID, rerr := h.deps.Jobs.RunManual(ctx, key, user.ID)
	if rerr != nil {
		err = rerr
		return nil, err
	}
	return map[string]any{
		"run_id":     runID,
		"status":     "started",
		"started_at": time.Now().UTC(),
	}, nil
}

func (h *handlers) jobGetRun(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	runID := c.Input("run_id")
	args := map[string]string{"run_id": runID}
	user, err := requireUser(ctx)
	defer func() { logOp(user, "job_get_run", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	run, rerr := h.deps.Jobs.GetRun(ctx, runID)
	if rerr != nil {
		err = rerr
		return nil, err
	}
	jobKey, jkErr := h.jobKeyForRun(ctx, run)
	if jkErr != nil {
		err = jkErr
		return nil, err
	}
	if !h.canAccessJob(ctx, user, jobKey) {
		err = errAccessDenied
		return nil, err
	}
	return map[string]any{
		"id":           run.ID,
		"job_key":      jobKey,
		"status":       string(run.Status),
		"result":       run.Result,
		"triggered_by": string(run.TriggeredBy),
		"started_at":   run.StartedAt,
		"ended_at":     run.EndedAt,
	}, nil
}

func (h *handlers) jobListRuns(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	limit := c.InputInt("limit")
	if limit <= 0 {
		limit = 20
	}
	args := map[string]any{"key": key, "limit": limit}
	user, err := requireUser(ctx)
	defer func() { logOp(user, "job_list_runs", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if !h.canAccessJob(ctx, user, key) {
		err = errAccessDenied
		return nil, err
	}
	runs, lerr := h.deps.Jobs.ListRuns(ctx, key, limit)
	if lerr != nil {
		err = lerr
		return nil, err
	}
	out := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		var dur int64
		if r.EndedAt != nil {
			dur = r.EndedAt.Sub(r.StartedAt).Milliseconds()
		}
		out = append(out, map[string]any{
			"id":           r.ID,
			"status":       string(r.Status),
			"triggered_by": string(r.TriggeredBy),
			"started_at":   r.StartedAt,
			"ended_at":     r.EndedAt,
			"duration_ms":  dur,
		})
	}
	return out, nil
}

func (h *handlers) jobKeyForRun(ctx context.Context, run *entity.JobRun) (string, error) {
	jobs, err := h.deps.Jobs.ListJobs(ctx)
	if err != nil {
		return "", err
	}
	for _, j := range jobs {
		if j.ID == run.JobID {
			return j.Key, nil
		}
	}
	return "", errors.New("parent job not found")
}

// canAccessJob mirrors login.Middleware.RequireJobAccess.
func (h *handlers) canAccessJob(ctx context.Context, user *entity.User, key string) bool {
	if user == nil {
		return false
	}
	return h.deps.Login.CanAccessTool(ctx, user, "/jobs/"+key, entity.VisibilityPrivate)
}

// ── tool_* ───────────────────────────────────────────────────────────

func (h *handlers) toolList(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireUser(ctx)
	defer func() { logOp(user, "tool_list", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(h.deps.Tools))
	for _, t := range h.deps.Tools {
		// Tool-category items only — jobs/connectors enumerate elsewhere.
		if t.Category == "job" || t.Category == "connector" {
			continue
		}
		if !h.deps.Login.CanAccessTool(ctx, user, t.Path, t.DefaultVisibility) {
			continue
		}
		hasCfg := len(h.deps.Configs.ListOwned(t.Key)) > 0
		out = append(out, map[string]any{
			"key":         t.Key,
			"name":        t.Name,
			"description": t.Description,
			"icon":        t.Icon,
			"category":    t.Category,
			"has_config":  hasCfg,
		})
	}
	return out, nil
}

func (h *handlers) toolGet(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	args := map[string]string{"key": key}
	user, err := requireUser(ctx)
	defer func() { logOp(user, "tool_get", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	t, ok := h.findTool(key)
	if !ok {
		err = fmt.Errorf("unknown tool %q", key)
		return nil, err
	}
	if !h.deps.Login.CanAccessTool(ctx, user, t.Path, t.DefaultVisibility) {
		err = errAccessDenied
		return nil, err
	}
	cfgs := h.deps.Configs.ListOwned(t.Key)
	return map[string]any{
		"meta": map[string]any{
			"key":         t.Key,
			"name":        t.Name,
			"description": t.Description,
			"icon":        t.Icon,
			"category":    t.Category,
		},
		"configs": configRowsOut(cfgs),
	}, nil
}

func (h *handlers) toolSetConfig(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	key := c.Input("key")
	configKey := c.Input("config_key")
	value := c.Input("value")
	args := map[string]string{"key": key, "config_key": configKey, "value": maskValueForLog(value)}
	user, err := requireUser(ctx)
	var before, after map[string]any
	defer func() {
		logOpDiff(user, "tool_set_config", args, before, after, err, time.Since(start))
	}()
	if err != nil {
		return nil, err
	}
	t, ok := h.findTool(key)
	if !ok {
		err = fmt.Errorf("unknown tool %q", key)
		return nil, err
	}
	if !h.deps.Login.CanAccessTool(ctx, user, t.Path, t.DefaultVisibility) {
		err = errAccessDenied
		return nil, err
	}
	row, ok := h.findOwnedRow(key, configKey)
	if !ok {
		err = fmt.Errorf("unknown config %s/%s", key, configKey)
		return nil, err
	}
	if row.Locked {
		err = errLockedRow
		return nil, err
	}
	if row.Required && strings.TrimSpace(value) == "" && !row.IsSecret {
		err = errRequiredEmpty
		return nil, err
	}
	before = configRowOut(row)
	if err = h.deps.Configs.SetOwned(ctx, key, configKey, value); err != nil {
		return nil, err
	}
	if newRow, ok := h.findOwnedRow(key, configKey); ok {
		after = configRowOut(newRow)
	}
	return map[string]any{"ok": true, "key": key, "config_key": configKey, "before": before, "after": after}, nil
}

func (h *handlers) findTool(key string) (tool.Tool, bool) {
	for _, t := range h.deps.Tools {
		if t.Key == key {
			return t, true
		}
	}
	return tool.Tool{}, false
}

func (h *handlers) findOwnedRow(owner, key string) (entity.Config, bool) {
	for _, r := range h.deps.Configs.ListOwned(owner) {
		if r.Key == key {
			return r, true
		}
	}
	return entity.Config{}, false
}

// ── connector_* ──────────────────────────────────────────────────────

func (h *handlers) connectorList(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireUser(ctx)
	defer func() { logOp(user, "connector_list", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	tagIDs := login.GetUserTagIDs(ctx)
	rows, err := h.deps.Connectors.ListVisibleTo(ctx, tagIDs, user.IsAdmin())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		mod, ok := h.deps.Connectors.Module(row.Key)
		if !ok {
			continue
		}
		states, _ := h.deps.Connectors.OperationStates(ctx, row.ID, row.Key)
		count := 0
		for _, op := range mod.Operations {
			if states[op.Key] {
				count++
			}
		}
		hasCfg := len(mod.Configs) > 0
		out = append(out, map[string]any{
			"id":          row.ID,
			"key":         row.Key,
			"label":       row.Label,
			"description": mod.Meta.Description,
			"icon":        mod.Meta.Icon,
			"status":      h.deps.Connectors.Status(row),
			"total_tools": count,
			"disabled":    row.Disabled,
			"has_config":  hasCfg,
		})
	}
	return out, nil
}

func (h *handlers) connectorGet(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	id := c.Input("id")
	args := map[string]string{"id": id}
	user, err := requireUser(ctx)
	defer func() { logOp(user, "connector_get", args, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	tagIDs := login.GetUserTagIDs(ctx)
	allowed, err := h.deps.Connectors.IsVisibleTo(ctx, id, tagIDs, user.IsAdmin())
	if err != nil {
		return nil, err
	}
	if !allowed {
		err = errAccessDenied
		return nil, err
	}
	row, gerr := h.deps.Connectors.Get(ctx, id)
	if gerr != nil {
		err = gerr
		return nil, err
	}
	mod, ok := h.deps.Connectors.Module(row.Key)
	if !ok {
		err = fmt.Errorf("connector module %q not registered", row.Key)
		return nil, err
	}
	cfgs := h.deps.Connectors.RowConfigs(*row)
	ops := make([]map[string]any, 0, len(mod.Operations))
	states, _ := h.deps.Connectors.OperationStates(ctx, row.ID, row.Key)
	for _, op := range mod.Operations {
		ops = append(ops, map[string]any{
			"key":         op.Key,
			"name":        op.Name,
			"description": op.Description,
			"destructive": op.Destructive,
			"enabled":     states[op.Key],
		})
	}
	return map[string]any{
		"meta": map[string]any{
			"id":          row.ID,
			"key":         row.Key,
			"label":       row.Label,
			"description": mod.Meta.Description,
			"icon":        mod.Meta.Icon,
			"disabled":    row.Disabled,
			"status":      h.deps.Connectors.Status(*row),
			"fixed":       mod.Meta.Fixed,
		},
		"configs":    configRowsOut(cfgs),
		"operations": ops,
	}, nil
}

func (h *handlers) connectorSetConfig(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	id := c.Input("id")
	configKey := c.Input("config_key")
	value := c.Input("value")
	args := map[string]string{"id": id, "config_key": configKey, "value": maskValueForLog(value)}
	user, err := requireUser(ctx)
	var before, after map[string]any
	defer func() {
		logOpDiff(user, "connector_set_config", args, before, after, err, time.Since(start))
	}()
	if err != nil {
		return nil, err
	}
	tagIDs := login.GetUserTagIDs(ctx)
	allowed, aerr := h.deps.Connectors.IsManageableBy(ctx, id, tagIDs, user.IsAdmin())
	if aerr != nil {
		err = aerr
		return nil, err
	}
	if !allowed {
		err = errAccessDenied
		return nil, err
	}
	row, gerr := h.deps.Connectors.Get(ctx, id)
	if gerr != nil {
		err = gerr
		return nil, err
	}
	rows := h.deps.Connectors.RowConfigs(*row)
	var target entity.Config
	found := false
	for _, r := range rows {
		if r.Key == configKey {
			target = r
			found = true
			break
		}
	}
	if !found {
		err = fmt.Errorf("unknown config %s/%s", id, configKey)
		return nil, err
	}
	if target.Locked {
		err = errLockedRow
		return nil, err
	}
	if target.Required && strings.TrimSpace(value) == "" && !target.IsSecret {
		err = errRequiredEmpty
		return nil, err
	}
	before = configRowOut(target)
	stored := h.deps.Connectors.LoadConfigs(*row)
	stored[configKey] = value
	if err = h.deps.Connectors.Update(ctx, row.ID, row.Label, stored, row.Disabled); err != nil {
		return nil, err
	}
	for _, r := range h.deps.Connectors.RowConfigs(*row) {
		if r.Key == configKey {
			after = configRowOut(r)
			break
		}
	}
	return map[string]any{"ok": true, "id": id, "config_key": configKey, "before": before, "after": after}, nil
}

// ── system_* ─────────────────────────────────────────────────────────

func (h *handlers) systemStatus(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "system_status", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if err = requireTray(); err != nil {
		return nil, err
	}
	return map[string]any{
		"server_running": processctl.IsServerRunning(),
		"server_port":    processctl.ServerPort(),
		"worker_running": processctl.IsWorkerRunning(),
		"run_mode":       runMode(),
	}, nil
}

func (h *handlers) systemServerStart(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "system_server_start", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if err = requireTray(); err != nil {
		return nil, err
	}
	if err = processctl.StartServer(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "port": processctl.ServerPort()}, nil
}

func (h *handlers) systemServerStop(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "system_server_stop", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if err = requireTray(); err != nil {
		return nil, err
	}
	if err = processctl.StopServer(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (h *handlers) systemWorkerStart(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "system_worker_start", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if err = requireTray(); err != nil {
		return nil, err
	}
	if err = processctl.StartWorker(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (h *handlers) systemWorkerStop(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "system_worker_stop", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if err = requireTray(); err != nil {
		return nil, err
	}
	if err = processctl.StopWorker(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (h *handlers) systemPrefsGet(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, "system_prefs_get", nil, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	if err = requireTray(); err != nil {
		return nil, err
	}
	cfg, lerr := userconfig.Load(h.deps.AppName)
	if lerr != nil {
		err = lerr
		return nil, err
	}
	return cfg, nil
}

func (h *handlers) systemPrefsSet(c *connector.Ctx) (any, error) {
	start := time.Now()
	ctx := c.Context()
	args := collectPrefsInput(c)
	user, err := requireAdmin(ctx)
	var before, after userconfig.Config
	defer func() {
		logOpDiff(user, "system_prefs_set", args, before, after, err, time.Since(start))
	}()
	if err != nil {
		return nil, err
	}
	if err = requireTray(); err != nil {
		return nil, err
	}
	cfg, lerr := userconfig.Load(h.deps.AppName)
	if lerr != nil {
		err = lerr
		return nil, err
	}
	before = cfg
	applyPrefsPatch(&cfg, c)
	if err = userconfig.Save(h.deps.AppName, cfg); err != nil {
		return nil, err
	}
	after = cfg
	return cfg, nil
}

// applyPrefsPatch merges only fields present in the LLM input. Empty
// strings on the input map are treated as "field not present" — wick
// reflects pointer fields to make this cheap, but Ctx.Input flattens
// to strings, so we read with explicit per-key has-value checks.
func applyPrefsPatch(cfg *userconfig.Config, c *connector.Ctx) {
	if v := c.Input("auto_start_app"); v != "" {
		cfg.AutoStartApp = c.InputBool("auto_start_app")
	}
	if v := c.Input("auto_start_server"); v != "" {
		cfg.AutoStartServer = c.InputBool("auto_start_server")
	}
	if v := c.Input("auto_start_worker"); v != "" {
		cfg.AutoStartWorker = c.InputBool("auto_start_worker")
	}
	if v := c.Input("auto_update"); v != "" {
		cfg.AutoUpdate = c.InputBool("auto_update")
	}
	if v := c.Input("port"); v != "" {
		cfg.Port = c.InputInt("port")
	}
	if v := c.Input("log_retention_days"); v != "" {
		cfg.LogRetentionDays = c.InputInt("log_retention_days")
	}
	if v := c.Input("database_path"); v != "" {
		cfg.DatabasePath = v
	}
}

func collectPrefsInput(c *connector.Ctx) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"auto_start_app", "auto_start_server", "auto_start_worker", "auto_update", "port", "log_retention_days", "database_path"} {
		if v := c.Input(k); v != "" {
			out[k] = v
		}
	}
	return out
}

func runMode() string {
	if processctl.IsManaged() {
		return "tray"
	}
	return "headless"
}

// maskValueForLog hides secrets from the audit log. Distinguishing
// secret rows here requires a key lookup the caller has already done;
// pass through wick_enc_ tokens (already opaque) and replace anything
// else with a fixed sentinel so the LLM-supplied plaintext never
// reaches mcp.log.
func maskValueForLog(v string) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "wick_enc_") || strings.HasPrefix(v, "wick_cenc_") {
		return v
	}
	return "***"
}
