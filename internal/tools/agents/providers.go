package agents

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// providersPage renders the Providers overview: one card per
// configured runtime instance (claude/codex/gemini × user-named
// profiles), live detect/version status, and the most recent spawn
// log files. The page is static (no SSE) so the user reloads to
// refresh — that matches the page reload model decided in the
// agents-design doc.
func providersPage(c *tool.Ctx) {
	if notReady(c) {
		return
	}

	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()
	statuses, err := provider.ProbeAll(ctx)
	if err != nil {
		log.Ctx(c.Context()).Error().Msgf("providers probe: %s", err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}

	const perPage = 10
	page := 1
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	var spawns []provider.SpawnLogFile
	hasNext := false
	if globalSpawnLog != nil {
		all, err := globalSpawnLog.List("", "", "")
		if err != nil {
			log.Ctx(c.Context()).Warn().Msgf("providers spawns list: %s", err.Error())
		} else {
			start := (page - 1) * perPage
			if start > len(all) {
				start = len(all)
			}
			end := start + perPage
			if end > len(all) {
				end = len(all)
			}
			spawns = all[start:end]
			hasNext = end < len(all)
		}
	}

	c.HTML(view.ProvidersPage(view.ProvidersVM{
		Base:          c.Base(),
		Statuses:      statuses,
		Spawns:        spawns,
		Page:          page,
		HasNext:       hasNext,
		PoolActive:    globalPool.Active(),
		PoolQueueLen:  globalPool.QueueLen(),
		PoolMax:       poolMaxConcurrent(),
		SupportedKeys: supportedTypeKeys(),
	}))
}

// saveProviderInstance creates or updates one named runtime instance
// from form fields. Empty BinaryPath = LookPath the canonical type
// name on PATH.
func saveProviderInstance(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	t := provider.Type(strings.TrimSpace(c.Form("type")))
	name := strings.TrimSpace(c.Form("name"))
	if name == "" {
		c.Error(http.StatusBadRequest, "instance name required")
		return
	}
	ins := provider.Instance{
		Type:      t,
		Name:      name,
		Binary:    strings.TrimSpace(c.Form("binary")),
		ExtraArgs: splitFields(c.Form("extra_args")),
		Env:       splitLines(c.Form("env")),
		Disabled:  c.Form("disabled") == "on" || c.Form("disabled") == "true",
	}
	if err := provider.Save(ins); err != nil {
		log.Ctx(c.Context()).Error().Msgf("save provider %s/%s: %s", t, name, err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
}

// deleteProviderInstance removes a named instance. Removing the last
// instance for a type is allowed — the next page load auto-seeds the
// canonical default.
func deleteProviderInstance(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	if err := provider.Delete(t, name); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// providerSpawnDetail renders the timeline of one spawn log file. The
// `file` path param is the bare filename (no directory) — the
// SpawnLogger resolves it under its own BaseDir.
func providerSpawnDetail(c *tool.Ctx) {
	if globalSpawnLog == nil {
		c.Error(http.StatusServiceUnavailable, "spawn logger not ready")
		return
	}
	name := c.PathValue("file")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		c.Error(http.StatusBadRequest, "invalid spawn log filename")
		return
	}
	all, err := globalSpawnLog.List("", "", "")
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	var meta provider.SpawnLogFile
	for _, f := range all {
		if filenameOf(f.Path) == name {
			meta = f
			break
		}
	}
	if meta.Path == "" {
		c.NotFound()
		return
	}
	events, err := globalSpawnLog.Read(meta.Path)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.HTML(view.ProviderSpawnDetail(view.ProviderSpawnDetailVM{
		Base:   c.Base(),
		File:   meta,
		Events: events,
	}))
}

// poolMaxConcurrent surfaces the live MaxConcurrent slot count. The
// pool keeps it private; we read through PoolConfig accessors that
// the pool exposes for tests + UI.
func poolMaxConcurrent() int {
	if globalPool == nil {
		return 0
	}
	return globalPool.MaxConcurrent()
}

// providerChoices probes every configured provider and returns only
// the healthy ones (binary on PATH + --version succeeded + not
// disabled). Used by every "pick a provider" form so users can't
// pick a provider that won't spawn.
func providerChoices(ctx context.Context) []view.ProviderChoiceVM {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	statuses, err := provider.ProbeAll(probeCtx)
	if err != nil {
		return nil
	}
	out := make([]view.ProviderChoiceVM, 0, len(statuses))
	for _, st := range statuses {
		if st.Instance.Disabled || !st.PathFound || st.VersionErr != "" {
			continue
		}
		out = append(out, view.ProviderChoiceVM{
			Type:    string(st.Instance.Type),
			Name:    st.Instance.Name,
			Version: st.Version,
		})
	}
	return out
}

func supportedTypeKeys() []string {
	out := make([]string, 0, len(provider.SupportedTypes()))
	for _, t := range provider.SupportedTypes() {
		out = append(out, string(t))
	}
	return out
}

func splitFields(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	out := make([]string, 0)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filenameOf(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
