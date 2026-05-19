package agents

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/capability"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/internal/mcpconfig"

	// Blank-import each provider sub-package so its init() fires when
	// the agents UI module loads. Without this, capability.Lookup
	// returns (zero, false) for codex/gemini and the Test button
	// errors with "provider not registered" even though the adapter
	// code exists. Mirrors the cmd/gate/main.go pattern.
	_ "github.com/yogasw/wick/internal/agents/provider/claude"
	_ "github.com/yogasw/wick/internal/agents/provider/codex"
	_ "github.com/yogasw/wick/internal/agents/provider/gemini"

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
	statuses, err := provider.LoadCached(ctx)
	if err != nil {
		log.Ctx(c.Context()).Error().Msgf("providers probe: %s", err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	// Annotate in-flight probes so the UI can disable buttons while
	// a background check is running. Cheap map lookup per row.
	for i := range statuses {
		statuses[i].Probing = capability.IsProbing(string(statuses[i].Instance.Type))
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

	mcpVM := buildMCPStatusVM()
	// Auto-install into detected clients that don't have wick yet.
	go autoInstallMCP(mcpVM.AppName)

	c.HTML(view.ProvidersPage(view.ProvidersVM{
		Layout:        sidebarVM(c, "providers", ""),
		Base:          c.Base(),
		Statuses:      statuses,
		Spawns:        spawns,
		Page:          page,
		HasNext:       hasNext,
		PoolActive:    globalPool.Active(),
		PoolQueueLen:  globalPool.QueueLen(),
		PoolMax:       poolMaxConcurrent(),
		SupportedKeys: supportedTypeKeys(),
		Gate:          gateStatusVM(),
		AutoRescan:    autoRescanEnabled(),
		MCP:           mcpVM,
	}))
}

// gateStatusVM converts the boot-time GateStatus + the live master
// switch + sub-policy modes into the view-model rendered by
// gateStatusCard. Note carries a one-sentence consequence summary so
// operators understand what each combination actually does at spawn
// time.
func gateStatusVM() view.GateStatusVM {
	s := GetGateStatus()
	configEnabled := masterGateEnabled()
	permMode := currentPermissionMode()
	// Permission bypass trumps the per-provider hook — spawner strips
	// the hook config when mode=bypass, so the gate cannot enforce
	// regardless of per-provider intent.
	bypass := permMode == "bypass"
	enabled := configEnabled && s.Binary != "" && !bypass
	vm := view.GateStatusVM{
		Enabled:        enabled,
		Binary:         s.Binary,
		Source:         s.Source,
		Reason:         s.Reason,
		PermissionMode: permMode,
		BypassLocked:   bypass,
	}
	switch {
	case !configEnabled:
		vm.Note = "Gate is off — permission prompts skipped, spawns run unguarded. Turn the master switch on to honour the permission policy below."
	case bypass:
		vm.Note = "Permission policy is set to bypass — spawns run unguarded so non-interactive channels (Slack/HTTP) don't hang on permission prompts. Switch to 'on' to gate per-provider hooks."
	case enabled:
		vm.Note = "Gate is on. Permission prompts route through the per-provider hook below."
	case configEnabled && s.Binary == "":
		vm.Note = "Gate is on in config but the gate binary did not resolve. Run `wick build` so the sibling sidecar or embedded fallback is available."
	default:
		vm.Note = "Gate binary not resolved — re-run `wick build` and reload."
	}
	return vm
}

// currentPermissionMode returns the active GateConfig.PermissionMode,
// defaulting to "on" when the row is empty so a fresh install enforces
// permission prompts out of the box.
func currentPermissionMode() string {
	if globalConfigs == nil {
		return "on"
	}
	v := globalConfigs.GetOwned("agents", "permission_mode")
	if v == "" {
		return "on"
	}
	return v
}

// toggleGate flips the agents.gate_enabled master switch AND
// cascades the new value into every per-provider Hooks[PreToolUse]
// flag. When turning ON, it also kicks off a background capability
// probe per provider so the UI badge reflects verified state without
// the user clicking Test on each card.
//
// Cascade semantic — single source of truth lives in the per-instance
// flag; the master toggle is a fan-out command, not a separate gate.
// Effect:
//
//   - OFF→ON: flip every instance's Hooks[PreToolUse].Enabled = true,
//     then spawn one goroutine per provider that runs the capability
//     probe and persists the result. Provider whose probe fails has
//     its Enabled flipped back to false so the spawner won't install
//     a hook that wouldn't be honored.
//
//   - ON→OFF: flip every instance's Hooks[PreToolUse].Enabled = false.
//     Capability state stays so re-enabling later doesn't lose the
//     last probe result. Spawners see Enabled=false → no hook install.
//
// Effect is immediate — next spawn reads the live per-instance flag.
func toggleGate(c *tool.Ctx) {
	if globalConfigs == nil {
		c.Error(http.StatusServiceUnavailable, "configs service not wired")
		return
	}
	// PermissionMode=bypass strips the per-provider hook config at spawn
	// time, so enabling the master switch in that state silently no-ops.
	// Refuse the toggle and tell the operator to flip permission_mode
	// back to "on" first.
	before, _ := strconv.ParseBool(globalConfigs.GetOwned("agents", "gate_enabled"))
	if !before && bypassPermissionsEnabled() {
		c.Error(http.StatusConflict, "permission_mode is set to bypass — switch it to 'on' in agents settings before turning the gate on")
		return
	}
	next := !before

	if err := globalConfigs.SetOwned(c.Context(), "agents", "gate_enabled", strconv.FormatBool(next)); err != nil {
		log.Ctx(c.Context()).Error().Msgf("toggle gate: %s", err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}

	// Cascade into every configured instance + auto-seed default rows
	// where the user hasn't created an instance yet. Iterate over the
	// supported types so a fresh install (no user-saved instances)
	// still ends up with the default <type>/<type> entries materialised.
	all, _ := provider.Load()
	for _, ins := range all {
		_ = provider.SetHookEnabled(ins.Type, ins.Name, provider.HookEventPreToolUse, next)
	}

	// Background probe per provider when turning ON. Fire-and-forget
	// goroutines: the page redirects immediately; results show up on
	// the next render as the disk-persisted capability state.
	if next {
		go runBackgroundProbeAll()
	}

	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
}

// saveGateModes writes GateConfig.PermissionMode from the gate card form.
// Constrained to a known enum; unknown values fall back to "on" so a
// malformed POST never bricks the gate into an unknown state.
//
// AskUserMode is not surfaced in the UI — the policy is controlled via
// system prompt instead. The field remains on GateConfig and respects
// whatever the config layer has stored (default "on").
func saveGateModes(c *tool.Ctx) {
	if globalConfigs == nil {
		c.Error(http.StatusServiceUnavailable, "configs service not wired")
		return
	}
	perm := strings.TrimSpace(c.Form("permission_mode"))
	if perm != "bypass" {
		perm = "on"
	}
	if err := globalConfigs.SetOwned(c.Context(), "agents", "permission_mode", perm); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
}

// runBackgroundProbeAll spawns one capability probe per configured
// instance, persists each result, and rolls back the Enabled flag for
// providers whose probe failed. Runs detached from the request so
// the user gets the page back immediately while probes finish in 5–30s
// each. New hooks added later (SessionStart etc.) plug in here without
// changing the toggle flow.
func runBackgroundProbeAll() {
	all, err := provider.Load()
	if err != nil {
		log.Warn().Err(err).Msg("agents.gate.toggle: load providers for probe")
		return
	}
	gateBin := GetGateStatus().Binary
	if gateBin == "" {
		log.Warn().Msg("agents.gate.toggle: gate binary unresolved, skipping background probe")
		return
	}
	for _, ins := range all {
		ins := ins
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			res := capability.HookCapabilityCheck(ctx, capability.CheckInput{
				ProviderName: string(ins.Type),
				GateBinary:   gateBin + " --probe-deny --provider=" + string(ins.Type),
			})
			provider.MergeHookCapability(ins.Type, ins.Name, provider.HookEventPreToolUse, provider.HookCapability{
				Supported: res.HookSupported,
				Verified:  res.HookVerified,
				ProbedAt:  res.HookProbedAt,
				Error:     res.HookError,
				Scope:     res.InterceptScope,
			})
			// Roll back intent if the probe failed — leaving Enabled=true
			// for a broken provider would silently disable its tooling
			// (hook installed but provider ignores the deny envelope).
			if !res.HookVerified {
				_ = provider.SetHookEnabled(ins.Type, ins.Name, provider.HookEventPreToolUse, false)
			}
		}()
	}
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
	if mode := strings.TrimSpace(c.Form("storage_mode")); mode != "" {
		ins.Storage = &provider.StorageConfig{
			Mode:            mode,
			SyncPath:        strings.TrimSpace(c.Form("storage_path")),
			IntervalSeconds: parseIntForm(c.Form("storage_interval")),
		}
	}
	if err := provider.Save(ins); err != nil {
		log.Ctx(c.Context()).Error().Msgf("save provider %s/%s: %s", t, name, err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
}

// syncProviderStorage triggers an immediate backup of one instance's
// credential files to the DB.
//
// POST /providers/{type}/{name}/sync
func syncProviderStorage(c *tool.Ctx) {
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not ready"})
		return
	}
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	ins, err := provider.Find(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if ins.Storage == nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "storage not configured for this instance"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()
	if err := globalSyncMgr.SyncOne(ctx, ins); err != nil {
		log.Ctx(c.Context()).Error().Msgf("manual sync %s/%s: %s", t, name, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "synced"})
}

// buildMCPStatusVM collects per-client install status for the MCP Wick card.
func buildMCPStatusVM() view.MCPStatusVM {
	cwd, _ := os.Getwd()
	name := appname.Resolve()
	blocklist := mcpBlocklist()
	clients := mcpconfig.AllClients(cwd)
	rows := make([]view.MCPClientStatusVM, 0, len(clients))
	for _, c := range clients {
		if !isDirPresent(c.Path) {
			continue
		}
		_, installed := mcpconfig.IsInstalled(c, name)
		rows = append(rows, view.MCPClientStatusVM{
			ID:          c.ID,
			Label:       c.Label,
			Detected:    true,
			Installed:   installed,
			Blocklisted: blocklist[c.ID],
			ConfigPath:  c.Path,
		})
	}
	return view.MCPStatusVM{AppName: name, Clients: rows}
}

// mcpBlocklist returns the set of client IDs the user has manually
// uninstalled. Read from agents.mcp_uninstalled_clients (comma-separated).
func mcpBlocklist() map[string]bool {
	if globalConfigs == nil {
		return nil
	}
	raw := globalConfigs.GetOwned("agents", "mcp_uninstalled_clients")
	if raw == "" {
		return nil
	}
	m := make(map[string]bool)
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			m[id] = true
		}
	}
	return m
}

// mcpAddBlocklist appends a client ID to the persistent blocklist.
func mcpAddBlocklist(ctx context.Context, clientID string) {
	if globalConfigs == nil {
		return
	}
	raw := globalConfigs.GetOwned("agents", "mcp_uninstalled_clients")
	ids := make(map[string]bool)
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			ids[id] = true
		}
	}
	ids[clientID] = true
	parts := make([]string, 0, len(ids))
	for id := range ids {
		parts = append(parts, id)
	}
	_ = globalConfigs.SetOwned(ctx, "agents", "mcp_uninstalled_clients", strings.Join(parts, ","))
}

// mcpRemoveBlocklist removes a client ID from the persistent blocklist
// (called when user manually installs a previously-uninstalled client).
func mcpRemoveBlocklist(ctx context.Context, clientID string) {
	if globalConfigs == nil {
		return
	}
	raw := globalConfigs.GetOwned("agents", "mcp_uninstalled_clients")
	var parts []string
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id != "" && id != clientID {
			parts = append(parts, id)
		}
	}
	_ = globalConfigs.SetOwned(ctx, "agents", "mcp_uninstalled_clients", strings.Join(parts, ","))
}

// isDirPresent returns true when the file's parent directory exists —
// same heuristic mcpconfig.Detected uses internally.
func isDirPresent(path string) bool {
	dir := path[:max(strings.LastIndexAny(path, `/\`), 0)]
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// autoInstallMCP installs wick into every detected client that doesn't
// have it yet, skipping blocklisted (manually-uninstalled) clients.
// Only runs once per app lifetime — guarded by agents.mcp_auto_installed.
func autoInstallMCP(name string) {
	if globalConfigs == nil {
		return
	}
	if globalConfigs.GetOwned("agents", "mcp_auto_installed") == "true" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	entry, err := mcpconfig.SelfEntry()
	if err != nil {
		log.Warn().Err(err).Msg("mcp auto-install: SelfEntry failed")
		return
	}
	blocklist := mcpBlocklist()
	ctx := context.Background()
	for _, c := range mcpconfig.Detected(cwd) {
		if blocklist[c.ID] {
			continue
		}
		_, installed := mcpconfig.IsInstalled(c, name)
		if installed {
			continue
		}
		if err := mcpconfig.Install(c, name, entry); err != nil {
			log.Warn().Err(err).Msgf("mcp auto-install: %s", c.ID)
		} else {
			log.Info().Msgf("mcp auto-install: installed %q into %s", name, c.Label)
		}
	}
	_ = globalConfigs.SetOwned(ctx, "agents", "mcp_auto_installed", "true")
}

// mcpInstallClient installs wick MCP entry into one client by ID and
// removes it from the blocklist so future auto-installs can reach it.
// POST /providers/mcp/{clientID}/install
func mcpInstallClient(c *tool.Ctx) {
	clientID := c.PathValue("clientID")
	cwd, _ := os.Getwd()
	cl, ok := mcpconfig.Find(cwd, clientID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "unknown client " + clientID})
		return
	}
	name := appname.Resolve()
	entry, err := mcpconfig.SelfEntry()
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := mcpconfig.Install(cl, name, entry); err != nil {
		log.Ctx(c.Context()).Error().Msgf("mcp install %s: %s", clientID, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	mcpRemoveBlocklist(c.Context(), clientID)
	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
}

// mcpUninstallClient removes wick MCP entry from one client by ID and
// adds it to the blocklist so auto-install never re-installs it.
// POST /providers/mcp/{clientID}/uninstall
func mcpUninstallClient(c *tool.Ctx) {
	clientID := c.PathValue("clientID")
	cwd, _ := os.Getwd()
	cl, ok := mcpconfig.Find(cwd, clientID)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "unknown client " + clientID})
		return
	}
	name := appname.Resolve()
	if err := mcpconfig.Uninstall(cl, name); err != nil {
		log.Ctx(c.Context()).Error().Msgf("mcp uninstall %s: %s", clientID, err.Error())
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	mcpAddBlocklist(c.Context(), clientID)
	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
}

func parseIntForm(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
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

// masterGateEnabled reads agents.gate_enabled from configs. Defaults
// to false so a fresh install with no row stays in the safest state
// (no hook installation until the operator opts in).
func masterGateEnabled() bool {
	if globalConfigs == nil {
		return false
	}
	b, _ := strconv.ParseBool(globalConfigs.GetOwned("agents", "gate_enabled"))
	return b
}

// bypassPermissionsEnabled reports whether the active permission
// policy is "bypass" — i.e. spawns run unguarded. Mirrors the legacy
// `agents.bypass_permissions` flag now that it lives under
// GateConfig.PermissionMode. Used by the UI lock + the toggleGate
// pre-check.
func bypassPermissionsEnabled() bool {
	return currentPermissionMode() == "bypass"
}

// autoRescanEnabled reads agents.auto_rescan from configs. Defaults
// to true when the row is empty so first-boot users get the standard
// "stale cache → background re-probe" behaviour without ceremony.
func autoRescanEnabled() bool {
	if globalConfigs == nil {
		return true
	}
	return globalConfigs.GetOwned("agents", "auto_rescan") != "false"
}


// rescanAllProviders forces a fresh path-scan + version probe for
// every configured instance, persisting results to the cache. Used
// by the "Rescan all" button on the Providers page when the user
// just installed a new CLI and doesn't want to wait for the 24h
// auto-refresh.
func rescanAllProviders(c *tool.Ctx) {
	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
	defer cancel()
	provider.RescanAll(ctx)
	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
}

// probeProviderGate runs the end-to-end gate-honor check on one
// provider instance: spawns claude with a force-deny PreToolUse
// hook, asks it to touch a sentinel, and reports whether the file
// got created. Used by the per-card "Test gate" button so the user
// can verify their installed claude actually honors the deny
// envelope before relying on the approval modal.
//
// Only meaningful for `claude` instances today — codex/gemini have
// different (or no) hook contracts. We still run the probe for
// non-claude rows so the UI can return a "not applicable" message
// in one place.
func probeProviderGate(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	if t == "" || name == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "type and name required"})
		return
	}
	if t != "claude" {
		c.JSON(http.StatusOK, gate.ProbeResult{
			Supported: false,
			Reason:    "gate probe only applies to `claude` instances",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Context(), 60*time.Second)
	defer cancel()

	statuses, err := provider.LoadCached(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var claudeBin string
	for _, st := range statuses {
		if st.Instance.Type == t && st.Instance.Name == name {
			claudeBin = st.Path
			break
		}
	}
	if claudeBin == "" {
		c.JSON(http.StatusOK, gate.ProbeResult{
			Supported: false,
			Reason:    "claude binary not resolved on PATH for this instance",
		})
		return
	}

	gateBin := GetGateStatus().Binary
	res := gate.ProbeGateSupport(ctx, claudeBin, gateBin)
	c.JSON(http.StatusOK, res)
}

// enableProviderHook runs the capability probe for one hook event,
// and IF the probe verifies, flips the user's per-instance enable
// intent so subsequent spawns install the hook config. Single-click
// "enable" UX: user clicks Enable, wick probes + persists in one go;
// on failure the intent stays off and the error surfaces inline.
//
// Path: POST /agents/providers/{type}/{name}/hooks/{event}/enable
func enableProviderHook(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	t, name, event, ok := parseHookParams(c)
	if !ok {
		return
	}

	// Defensive: refuse per-provider Enable while master switch is off
	// or bypass mode is on. UI hides the button in either state, so
	// reaching here means a direct curl / stale tab — surface the
	// constraint as a 409 so the caller knows what's wrong.
	if bypassPermissionsEnabled() {
		c.JSON(http.StatusConflict, map[string]string{
			"error": "bypass permissions is on — disable it in agents settings before enabling per-provider gates",
		})
		return
	}
	if !masterGateEnabled() {
		c.JSON(http.StatusConflict, map[string]string{
			"error": "master gate is off — turn the global Command Gate on before enabling individual providers",
		})
		return
	}

	res, hookCap := runCapabilityProbe(c, t)

	// Persist capability snapshot regardless of outcome — UI badge
	// reflects the probe state even when the user can't enable yet.
	provider.MergeHookCapability(t, name, event, hookCap)

	// Only flip the user's intent when the probe verified. A failed
	// probe leaves intent off so a half-broken setup doesn't silently
	// gate every future spawn.
	if res.HookVerified {
		if err := provider.SetHookEnabled(t, name, event, true); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, map[string]any{
		"enabled":   res.HookVerified, // true iff probe passed AND intent persisted
		"verified":  res.HookVerified,
		"supported": res.HookSupported,
		"probed_at": res.HookProbedAt.Format(time.RFC3339),
		"error":     res.HookError,
		"scope":     res.InterceptScope,
		"event":     event,
	})
}

// disableProviderHook flips the user's per-instance enable intent off
// for one hook event. Does NOT re-probe — capability state untouched.
//
// Path: POST /agents/providers/{type}/{name}/hooks/{event}/disable
func disableProviderHook(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	t, name, event, ok := parseHookParams(c)
	if !ok {
		return
	}
	if err := provider.SetHookEnabled(t, name, event, false); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"enabled": false, "event": event})
}

// parseHookParams extracts and defaults the {type, name, event} path
// params shared by all hook-capability handlers. Sends a 400 to the
// client and returns ok=false when required params are missing.
func parseHookParams(c *tool.Ctx) (t provider.Type, name, event string, ok bool) {
	t = provider.Type(c.PathValue("type"))
	name = c.PathValue("name")
	event = c.PathValue("event")
	if event == "" {
		event = provider.HookEventPreToolUse
	}
	if t == "" || name == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "type and name required"})
		return "", "", "", false
	}
	return t, name, event, true
}

// runCapabilityProbe is the shared probe path used by both /check and
// /enable. Returns the raw probe result plus a provider.HookCapability
// ready to feed into MergeHookCapability.
func runCapabilityProbe(c *tool.Ctx, t provider.Type) (capability.CheckResult, provider.HookCapability) {
	gateBin := GetGateStatus().Binary
	res := capability.CheckResult{}
	if gateBin != "" {
		ctx, cancel := context.WithTimeout(c.Context(), 60*time.Second)
		defer cancel()
		res = capability.HookCapabilityCheck(ctx, capability.CheckInput{
			ProviderName: string(t),
			GateBinary:   gateBin + " --probe-deny --provider=" + string(t),
		})
	} else {
		res.HookError = "gate binary not resolved — run `wick build`"
	}
	return res, provider.HookCapability{
		Supported: res.HookSupported,
		Verified:  res.HookVerified,
		ProbedAt:  res.HookProbedAt,
		Error:     res.HookError,
		Scope:     res.InterceptScope,
	}
}

// checkProviderHook runs the capability probe for one hook event on
// one provider instance. The handler is provider-agnostic — it looks
// up the registered Writer + Prober for the named provider, spawns
// the binary in a throwaway workspace with a force-deny hook, and
// reports whether the deny envelope was honored.
//
// Path: POST /agents/providers/{type}/{name}/hooks/{event}/check
//
// The result is merged into the persisted ProviderStatus so the next
// page render reflects the verified state without re-probing. Empty
// event defaults to PreToolUse (the command gate). The merge keeps
// version/path fields intact — see provider.MergeHookCapability.
// Unlike /enable, this handler does NOT change the user's intent
// flag — it's a pure "did we verify the deny?" probe.
func checkProviderHook(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	t, name, event, ok := parseHookParams(c)
	if !ok {
		return
	}

	res, hookCap := runCapabilityProbe(c, t)
	provider.MergeHookCapability(t, name, event, hookCap)

	c.JSON(http.StatusOK, map[string]any{
		"supported": res.HookSupported,
		"verified":  res.HookVerified,
		"probed_at": res.HookProbedAt.Format(time.RFC3339),
		"error":     res.HookError,
		"scope":     res.InterceptScope,
		"event":     event,
	})
}

// rescanOneProvider re-probes a single instance. Used by the per-card
// Rescan button so the user can refresh just the row they care about.
func rescanOneProvider(c *tool.Ctx) {
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	if t == "" || name == "" {
		c.Error(http.StatusBadRequest, "type and name required")
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), 15*time.Second)
	defer cancel()
	provider.RescanOne(ctx, t, name)
	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
}

// toggleAutoRescan flips agents.auto_rescan in configs. When off, the
// background staleness re-probe stops; user must hit Rescan manually.
func toggleAutoRescan(c *tool.Ctx) {
	if globalConfigs == nil {
		c.Error(http.StatusServiceUnavailable, "configs service not wired")
		return
	}
	before, _ := strconv.ParseBool(globalConfigs.GetOwned("agents", "auto_rescan"))
	next := strconv.FormatBool(!before)
	if err := globalConfigs.SetOwned(c.Context(), "agents", "auto_rescan", next); err != nil {
		log.Ctx(c.Context()).Error().Msgf("toggle auto-rescan: %s", err.Error())
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/providers", http.StatusSeeOther)
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
		Layout: sidebarVM(c, "providers", ""),
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
	statuses, err := provider.ProbeAllCached(probeCtx)
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

// providerChoicesCached reads provider status from the persistent cache
// (no subprocess probe). Used by pages that only need the provider list
// for a form dropdown — accuracy of version/path is not critical there.
func providerChoicesCached(ctx context.Context) []view.ProviderChoiceVM {
	statuses, err := provider.LoadCached(ctx)
	if err != nil {
		return nil
	}
	out := make([]view.ProviderChoiceVM, 0, len(statuses))
	for _, st := range statuses {
		if st.Instance.Disabled {
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
