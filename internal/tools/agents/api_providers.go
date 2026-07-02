package agents

import (
	"context"
	"net/http"
	"time"

	"github.com/yogasw/wick/internal/agents/capability"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/tools/agents/view"
	pkgentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
)

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// ProviderCapDTO is the used / effective-max slot count for one provider.
type ProviderCapDTO struct {
	Used      int  `json:"used"`
	Max       int  `json:"max"`
	Unlimited bool `json:"unlimited"`
}

// LiveProcessDTO is one active spawn entry.
type LiveProcessDTO struct {
	SessionID string `json:"session_id"`
	AgentName string `json:"agent_name"`
	PID       int    `json:"pid"`
	Lifecycle string `json:"lifecycle"`
	Substate  string `json:"substate"`
}

// HookCapabilityDTO is the per-event hook capability state for one provider.
type HookCapabilityDTO struct {
	Supported bool   `json:"supported"`
	Verified  bool   `json:"verified"`
	ProbedAt  string `json:"probed_at,omitempty"`
	Error     string `json:"error,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

// ProviderInstanceDTO is the static config of one instance.
type ProviderInstanceDTO struct {
	Type          string `json:"type"`
	Name          string `json:"name"`
	Binary        string `json:"binary"`
	Disabled      bool   `json:"disabled"`
	MaxConcurrent int    `json:"max_concurrent"`
	SendMode      string `json:"send_mode"`
}

// ProviderStatusDTO is one provider card's data: instance config + live status.
type ProviderStatusDTO struct {
	Instance    ProviderInstanceDTO          `json:"instance"`
	Path        string                       `json:"path"`
	PathFound   bool                         `json:"path_found"`
	Version     string                       `json:"version"`
	VersionErr  string                       `json:"version_err,omitempty"`
	Probing     bool                         `json:"probing"`
	Hooks       map[string]HookCapabilityDTO `json:"hooks"`
	Cap         ProviderCapDTO               `json:"cap"`
	HookEnabled map[string]bool              `json:"hook_enabled"`
}

// SpawnLogFileDTO is a parsed spawn log file entry.
type SpawnLogFileDTO struct {
	Path             string `json:"path"`
	ProviderType     string `json:"provider_type"`
	ProviderName     string `json:"provider_name"`
	SessionID        string `json:"session_id"`
	StartedAt        string `json:"started_at"`
	PID              int    `json:"pid,omitempty"`
	Origin           string `json:"origin,omitempty"`
	FirstUserMessage string `json:"first_user_message,omitempty"`
	Binary           string `json:"binary,omitempty"`
	ExitReason       string `json:"exit_reason,omitempty"`
}

// SpawnEventDTO is one event line from a spawn log's timeline.
type SpawnEventDTO struct {
	Type             string   `json:"type"`
	At               string   `json:"at"`
	ProviderType     string   `json:"provider_type,omitempty"`
	ProviderName     string   `json:"provider_name,omitempty"`
	AgentName        string   `json:"agent_name,omitempty"`
	Workspace        string   `json:"workspace,omitempty"`
	ResumeID         string   `json:"resume_id,omitempty"`
	Binary           string   `json:"binary,omitempty"`
	Args             []string `json:"args,omitempty"`
	Env              []string `json:"env,omitempty"`
	PID              int      `json:"pid,omitempty"`
	Origin           string   `json:"origin,omitempty"`
	FirstUserMessage string   `json:"first_user_message,omitempty"`
	ExitReason       string   `json:"exit_reason,omitempty"`
	DurationMs       int64    `json:"duration_ms,omitempty"`
	Error            string   `json:"error,omitempty"`
	Message          string   `json:"message,omitempty"`
}

// SpawnDetailResponse is the full spawn-log detail: metadata, the event
// timeline, whether the session was since deleted, and the MASKED reproduce
// commands keyed by view.ReproKey (shell-mode-path). Unmasked variants come
// from the separate reveal endpoint.
type SpawnDetailResponse struct {
	File           SpawnLogFileDTO   `json:"file"`
	Events         []SpawnEventDTO   `json:"events"`
	SessionDeleted bool              `json:"session_deleted"`
	Repro          map[string]string `json:"repro"`
}

func spawnEventDTO(e provider.SpawnEvent) SpawnEventDTO {
	return SpawnEventDTO{
		Type:             e.Type,
		At:               e.At.UTC().Format(time.RFC3339),
		ProviderType:     e.ProviderType,
		ProviderName:     e.ProviderName,
		AgentName:        e.AgentName,
		Workspace:        e.Workspace,
		ResumeID:         e.ResumeID,
		Binary:           e.Binary,
		Args:             e.Args,
		Env:              e.Env,
		PID:              e.PID,
		Origin:           e.Origin,
		FirstUserMessage: e.FirstUserMessage,
		ExitReason:       e.ExitReason,
		DurationMs:       e.DurationMs,
		Error:            e.Error,
		Message:          e.Message,
	}
}

// MCPClientDTO is one MCP client install state.
type MCPClientDTO struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Detected    bool   `json:"detected"`
	Installed   bool   `json:"installed"`
	Blocklisted bool   `json:"blocklisted"`
	ConfigPath  string `json:"config_path"`
}

// MCPStatusDTO is the aggregate MCP Wick card state.
type MCPStatusDTO struct {
	AppName string         `json:"app_name"`
	Clients []MCPClientDTO `json:"clients"`
}

// GateStatusDTO is the gate policy state.
type GateStatusDTO struct {
	Enabled        bool   `json:"enabled"`
	Binary         string `json:"binary"`
	Source         string `json:"source"`
	Reason         string `json:"reason,omitempty"`
	Note           string `json:"note"`
	PermissionMode string `json:"permission_mode"`
	BypassLocked   bool   `json:"bypass_locked"`
}

// ProvidersListResponse is the JSON envelope for GET /api/providers.
type ProvidersListResponse struct {
	Providers     []ProviderStatusDTO `json:"providers"`
	Gate          GateStatusDTO       `json:"gate"`
	Spawns        []SpawnLogFileDTO   `json:"spawns"`
	MCPClients    MCPStatusDTO        `json:"mcp"`
	AutoRescan    bool                `json:"auto_rescan"`
	PoolActive    int                 `json:"pool_active"`
	PoolQueueLen  int                 `json:"pool_queue_len"`
	PoolMax       int                 `json:"pool_max"`
	LiveProcesses []LiveProcessDTO    `json:"live_processes"`
	SupportedKeys []string            `json:"supported_keys"`
}

// ConfigFieldDTO is one config row for the detail page (secret values masked).
type ConfigFieldDTO struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Type        string `json:"type"`
	Options     string `json:"options,omitempty"`
	IsSecret    bool   `json:"is_secret"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// ProviderDetailResponse is the JSON envelope for GET /api/providers/{type}/{name}.
type ProviderDetailResponse struct {
	Instance     ProviderInstanceDTO          `json:"instance"`
	Path         string                       `json:"path"`
	PathFound    bool                         `json:"path_found"`
	Version      string                       `json:"version"`
	VersionErr   string                       `json:"version_err,omitempty"`
	Probing      bool                         `json:"probing"`
	Hooks        map[string]HookCapabilityDTO `json:"hooks"`
	HookEnabled  map[string]bool              `json:"hook_enabled"`
	Gate         GateStatusDTO                `json:"gate"`
	GlobalMax    int                          `json:"global_max"`
	ActiveCount  int                          `json:"active_count"`
	ActivePIDs   []LiveProcessDTO             `json:"active_pids"`
	ConfigFields []ConfigFieldDTO             `json:"config_fields"`
	Spawns       []SpawnLogFileDTO            `json:"spawns"`
	Page         int                          `json:"page"`
	HasNext      bool                         `json:"has_next"`
	Router9      Router9DetailDTO             `json:"router9"`
}

// Router9DetailDTO carries the instance's current 9router settings so the
// detail page can seed its widget. The API key is never returned — only a
// flag indicating one is stored.
type Router9DetailDTO struct {
	Supported bool              `json:"supported"`
	Enabled   bool              `json:"enabled"`
	Models    map[string]string `json:"models"`
	KeySet    bool              `json:"key_set"`
}

// StorageFileDTO is one storage file row (without the binary content blob).
type StorageFileDTO struct {
	ID            uint   `json:"id"`
	ProviderType  string `json:"provider_type"`
	InstanceName  string `json:"instance_name"`
	RelPath       string `json:"rel_path"`
	Name          string `json:"name"`
	IsDir         bool   `json:"is_dir"`
	Size          int    `json:"size"`
	SyncedAt      string `json:"synced_at"`
	RetentionDays int    `json:"retention_days"`
}

// StorageResponse is the JSON envelope for GET /api/providers/storage.
type StorageResponse struct {
	Files          []StorageFileDTO `json:"files"`
	FilterProvider string           `json:"filter_provider,omitempty"`
	FilterInstance string           `json:"filter_instance,omitempty"`
	ProviderTypes  []string         `json:"provider_types"`
}

/* ── converters ─────────────────────────────────────────────────────────── */

// gateStatusDTO converts the view-model gate state to the DTO form.
func gateStatusDTO(vm view.GateStatusVM) GateStatusDTO {
	return GateStatusDTO{
		Enabled:        vm.Enabled,
		Binary:         vm.Binary,
		Source:         vm.Source,
		Reason:         vm.Reason,
		Note:           vm.Note,
		PermissionMode: vm.PermissionMode,
		BypassLocked:   vm.BypassLocked,
	}
}

// hookCapabilityDTO converts a provider.HookCapability to its DTO.
func hookCapabilityDTO(h provider.HookCapability) HookCapabilityDTO {
	dto := HookCapabilityDTO{
		Supported: h.Supported,
		Verified:  h.Verified,
		Error:     h.Error,
		Scope:     h.Scope,
	}
	if !h.ProbedAt.IsZero() {
		dto.ProbedAt = h.ProbedAt.UTC().Format(time.RFC3339)
	}
	return dto
}

// providerStatusDTO converts a provider.Status to its DTO including capacities.
func providerStatusDTO(st provider.Status, caps map[string]view.ProviderCapVM) ProviderStatusDTO {
	hooksDTO := make(map[string]HookCapabilityDTO, len(st.Hooks))
	for k, v := range st.Hooks {
		hooksDTO[k] = hookCapabilityDTO(v)
	}
	hookEnabled := map[string]bool{
		provider.HookEventPreToolUse: st.Instance.HookEnabled(provider.HookEventPreToolUse),
	}

	key := string(st.Instance.Type) + "/" + st.Instance.Name
	capVM := caps[key]
	capDTO := ProviderCapDTO{
		Used:      capVM.Used,
		Max:       capVM.Max,
		Unlimited: capVM.Unlimited,
	}

	return ProviderStatusDTO{
		Instance: ProviderInstanceDTO{
			Type:          string(st.Instance.Type),
			Name:          st.Instance.Name,
			Binary:        st.Instance.Binary,
			Disabled:      st.Instance.Disabled,
			MaxConcurrent: st.Instance.MaxConcurrent,
			SendMode:      st.Instance.SendMode,
		},
		Path:        st.Path,
		PathFound:   st.PathFound,
		Version:     st.Version,
		VersionErr:  st.VersionErr,
		Probing:     st.Probing,
		Hooks:       hooksDTO,
		Cap:         capDTO,
		HookEnabled: hookEnabled,
	}
}

// spawnLogFileDTO converts a provider.SpawnLogFile to its DTO.
func spawnLogFileDTO(f provider.SpawnLogFile) SpawnLogFileDTO {
	return SpawnLogFileDTO{
		Path:             f.Path,
		ProviderType:     f.ProviderType,
		ProviderName:     f.ProviderName,
		SessionID:        f.SessionID,
		StartedAt:        f.StartedAt.UTC().Format(time.RFC3339),
		PID:              f.PID,
		Origin:           f.Origin,
		FirstUserMessage: f.FirstUserMessage,
		Binary:           f.Binary,
		ExitReason:       f.ExitReason,
	}
}

// mcpStatusDTO converts the view-model MCP state to the DTO form.
func mcpStatusDTO(vm view.MCPStatusVM) MCPStatusDTO {
	clients := make([]MCPClientDTO, 0, len(vm.Clients))
	for _, c := range vm.Clients {
		clients = append(clients, MCPClientDTO{
			ID:          c.ID,
			Label:       c.Label,
			Detected:    c.Detected,
			Installed:   c.Installed,
			Blocklisted: c.Blocklisted,
			ConfigPath:  c.ConfigPath,
		})
	}
	return MCPStatusDTO{AppName: vm.AppName, Clients: clients}
}

// liveProcessDTOs converts live process entries to DTOs.
func liveProcessDTOs() []LiveProcessDTO {
	vms := liveProcessesVM()
	out := make([]LiveProcessDTO, 0, len(vms))
	for _, v := range vms {
		out = append(out, LiveProcessDTO{
			SessionID: v.SessionID,
			AgentName: v.AgentName,
			PID:       v.PID,
			Lifecycle: v.Lifecycle,
			Substate:  v.Substate,
		})
	}
	return out
}

// spawnLogFileDTOs converts a slice of SpawnLogFile to DTOs.
func spawnLogFileDTOs(files []provider.SpawnLogFile) []SpawnLogFileDTO {
	out := make([]SpawnLogFileDTO, 0, len(files))
	for _, f := range files {
		out = append(out, spawnLogFileDTO(f))
	}
	return out
}

// configFieldDTOs converts entity.Config rows to DTOs, masking secret values.
// Secret fields have their Value replaced with "••••••••" when non-empty,
// following the same discipline applied across API endpoints that return
// provider config state.
func configFieldDTOs(rows []pkgentity.Config) []ConfigFieldDTO {
	out := make([]ConfigFieldDTO, 0, len(rows))
	for _, r := range rows {
		v := r.Value
		if r.IsSecret && v != "" {
			v = "••••••••"
		}
		out = append(out, ConfigFieldDTO{
			Key:         r.Key,
			Value:       v,
			Type:        r.Type,
			Options:     r.Options,
			IsSecret:    r.IsSecret,
			Description: r.Description,
			Required:    r.Required,
		})
	}
	return out
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// apiProvidersList handles GET /api/providers and returns the full providers
// overview payload consumed by the Providers SPA page.
func apiProvidersList(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !requireAdmin(c) {
		return
	}

	ctx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
	defer cancel()

	statuses, err := provider.LoadCached(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for i := range statuses {
		statuses[i].Probing = capability.IsProbing(string(statuses[i].Instance.Type))
	}

	if globalSpawnLog != nil {
		_ = globalSpawnLog.Prune(provider.MaxSpawnLogs)
	}

	caps := providerCapacities()
	providerDTOs := make([]ProviderStatusDTO, 0, len(statuses))
	for _, st := range statuses {
		providerDTOs = append(providerDTOs, providerStatusDTO(st, caps))
	}

	var spawnDTOs []SpawnLogFileDTO
	if globalSpawnLog != nil {
		raw, _ := globalSpawnLog.List("", "", "")
		spawnDTOs = spawnLogFileDTOs(normalizeSpawnStatus(raw))
	}
	if spawnDTOs == nil {
		spawnDTOs = []SpawnLogFileDTO{}
	}

	poolActive := 0
	poolQueueLen := 0
	if globalPool != nil {
		poolActive = globalPool.Active()
		poolQueueLen = globalPool.QueueLen()
	}

	gateVM := gateStatusVM()
	mcpVM := buildMCPStatusVM()

	c.JSON(http.StatusOK, ProvidersListResponse{
		Providers:     providerDTOs,
		Gate:          gateStatusDTO(gateVM),
		Spawns:        spawnDTOs,
		MCPClients:    mcpStatusDTO(mcpVM),
		AutoRescan:    autoRescanEnabled(),
		PoolActive:    poolActive,
		PoolQueueLen:  poolQueueLen,
		PoolMax:       poolMaxConcurrent(),
		LiveProcesses: liveProcessDTOs(),
		SupportedKeys: supportedTypeKeys(),
	})
}

// apiProviderDetail handles GET /api/providers/{type}/{name} and returns the
// detail page payload for one provider instance. Secret config values are
// masked — never echoed in plain text.
func apiProviderDetail(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !requireAdmin(c) {
		return
	}

	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	ins, err := provider.Find(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	st := provider.Probe(ctx, ins)

	var activePIDs []LiveProcessDTO
	if globalPool != nil {
		for _, e := range globalPool.ActiveSnapshot() {
			activePIDs = append(activePIDs, LiveProcessDTO{
				SessionID: e.SessionID,
				AgentName: e.AgentName,
				PID:       e.PID,
				Lifecycle: e.Lifecycle,
				Substate:  e.Substate,
			})
		}
	}
	if activePIDs == nil {
		activePIDs = []LiveProcessDTO{}
	}

	const perPage = 20
	page := 1
	if v := c.Query("page"); v != "" {
		if n := parseInt(v); n > 0 {
			page = n
		}
	}

	var spawnDTOs []SpawnLogFileDTO
	hasNext := false
	if globalSpawnLog != nil {
		raw, _ := globalSpawnLog.List(string(t), name, "")
		all := normalizeSpawnStatus(raw)
		start := (page - 1) * perPage
		if start > len(all) {
			start = len(all)
		}
		end := start + perPage
		if end > len(all) {
			end = len(all)
		}
		spawnDTOs = spawnLogFileDTOs(all[start:end])
		hasNext = end < len(all)
	}
	if spawnDTOs == nil {
		spawnDTOs = []SpawnLogFileDTO{}
	}

	hooksDTO := make(map[string]HookCapabilityDTO, len(st.Hooks))
	for k, v := range st.Hooks {
		hooksDTO[k] = hookCapabilityDTO(v)
	}
	hookEnabled := map[string]bool{
		provider.HookEventPreToolUse: st.Instance.HookEnabled(provider.HookEventPreToolUse),
	}

	gateVM := gateStatusVM()

	c.JSON(http.StatusOK, ProviderDetailResponse{
		Instance: ProviderInstanceDTO{
			Type:          string(st.Instance.Type),
			Name:          st.Instance.Name,
			Binary:        st.Instance.Binary,
			Disabled:      st.Instance.Disabled,
			MaxConcurrent: st.Instance.MaxConcurrent,
			SendMode:      st.Instance.SendMode,
		},
		Path:         st.Path,
		PathFound:    st.PathFound,
		Version:      st.Version,
		VersionErr:   st.VersionErr,
		Probing:      st.Probing,
		Hooks:        hooksDTO,
		HookEnabled:  hookEnabled,
		Gate:         gateStatusDTO(gateVM),
		GlobalMax:    poolMaxConcurrent(),
		ActiveCount:  len(activePIDs),
		ActivePIDs:   activePIDs,
		ConfigFields: configFieldDTOs(provider.SeedInstanceConfig(st.Instance)),
		Spawns:       spawnDTOs,
		Page:         page,
		HasNext:      hasNext,
		Router9:      router9DetailDTO(st.Instance),
	})
}

// router9DetailDTO projects an instance's 9router settings for the FE.
// The stored API key is never surfaced — only KeySet.
func router9DetailDTO(ins provider.Instance) Router9DetailDTO {
	models := map[string]string{}
	for k, v := range ins.Router9Models {
		models[k] = v
	}
	return Router9DetailDTO{
		Supported: len(provider.Router9Slots(ins.Type)) > 0,
		Enabled:   ins.Use9router,
		Models:    models,
		KeySet:    ins.Router9APIKey != "",
	}
}

// apiProvidersStorage handles GET /api/providers/storage and returns the
// storage manager page payload consumed by the Storage SPA page.
func apiProvidersStorage(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	if globalSyncMgr == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sync manager not ready"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	files, err := globalSyncMgr.ListAll(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	filterProvider := c.Query("provider")
	filterInstance := c.Query("instance")

	fileDTOs := make([]StorageFileDTO, 0, len(files))
	for _, f := range files {
		if filterProvider != "" && f.ProviderType != filterProvider {
			continue
		}
		if filterInstance != "" && f.InstanceName != filterInstance {
			continue
		}
		fileDTOs = append(fileDTOs, StorageFileDTO{
			ID:            f.ID,
			ProviderType:  f.ProviderType,
			InstanceName:  f.InstanceName,
			RelPath:       f.RelPath,
			Name:          f.Name,
			IsDir:         f.IsDir,
			Size:          f.Size,
			SyncedAt:      f.SyncedAt.UTC().Format(time.RFC3339),
			RetentionDays: f.RetentionDays,
		})
	}
	if fileDTOs == nil {
		fileDTOs = []StorageFileDTO{}
	}

	types := make([]string, 0)
	seen := map[string]bool{}
	for _, t := range provider.SupportedTypes() {
		k := string(t)
		if !seen[k] {
			types = append(types, k)
			seen[k] = true
		}
	}

	c.JSON(http.StatusOK, StorageResponse{
		Files:          fileDTOs,
		FilterProvider: filterProvider,
		FilterInstance: filterInstance,
		ProviderTypes:  types,
	})
}

/* ── helpers ─────────────────────────────────────────────────────────────── */

// parseInt parses a decimal string, returning 0 on error.
func parseInt(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
