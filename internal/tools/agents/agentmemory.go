package agents

// Agent Memory glue that needs the agents package's globals. The feature
// itself lives in the self-contained internal/agents/agentmemory package (+
// one folder per backend under it); this file backs its ConfigStore with the
// app config service and exposes the boot helpers server.go wires. The blank
// import registers the built-in backend (ai-memory).
//
// Deliberately thinner than airouter.go next door: the panel is wick's own FE
// against these endpoints, so there is no proxy mount at all and the backend's
// own web UI is never exposed at the wick root (PLAN §13.2).

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/agentmemory"
	_ "github.com/yogasw/wick/internal/agents/agentmemory/aimemory"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// agentMemoryConfigStore implements agentmemory.ConfigStore over
// globalConfigs. Keys live under the agents owner:
//
//	agentmemory_enabled                 master switch
//	agentmemory_data_dir                the store, shared by every backend
//	agentmemory_backfill_max_sessions   wick's backfill cap
//	agentmemory_<id>_port               per-backend daemon port
//	agentmemory_<id>_enable_web         per-backend web API (default on)
//	agentmemory_<id>_autostart          per-backend autostart
//	agentmemory_<id>_tuning             per-backend daemon tuning, as JSON
//
// The store is one key rather than one per backend because it is a
// daemon-level setting: a single daemon already holds every workspace and
// project, and the separation that matters happens on the workspace/project
// axis (PLAN §19). agentmemory_enabled and agentmemory_data_dir are the same
// keys server.go already reads for the project marker — one key, one meaning.
type agentMemoryConfigStore struct{}

func (agentMemoryConfigStore) Enabled() bool { return AgentMemoryEnabled() }

func (agentMemoryConfigStore) ReadAllowed(ctx context.Context) bool {
	return agentMemoryViewer(ctx)
}

func (agentMemoryConfigStore) ManageAllowed(ctx context.Context) bool {
	return agentMemoryAdminOnly(ctx)
}

func (agentMemoryConfigStore) Settings(id string) agentmemory.Settings {
	if globalConfigs == nil {
		return agentmemory.Settings{}
	}
	return agentmemory.Settings{
		DataDir:             globalConfigs.GetOwned("agents", "agentmemory_data_dir"),
		Port:                cfgInt("agentmemory_" + id + "_port"),
		EnableWeb:           cfgBool("agentmemory_"+id+"_enable_web", true),
		Autostart:           globalConfigs.GetOwned("agents", "agentmemory_"+id+"_autostart") == "true",
		BackfillMaxSessions: cfgInt("agentmemory_backfill_max_sessions"),
		Tuning:              readTuning(id),
	}
}

// readTuning loads the tuning block. It is ONE JSON row rather than a config
// key per field: the block is ~30 knobs that are only ever read and written
// together, and thirty keys would mean thirty writes per save and thirty
// migrations the first time a field is renamed.
//
// A row that will not parse yields the zero value, which means "every field
// unset" — and an unset field is not sent to the daemon at all, so a corrupt
// row degrades to the backend's own config file rather than to a daemon
// configured with zeros.
func readTuning(id string) agentmemory.Tuning {
	var t agentmemory.Tuning
	raw := globalConfigs.GetOwned("agents", "agentmemory_"+id+"_tuning")
	if strings.TrimSpace(raw) == "" {
		return t
	}
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return agentmemory.Tuning{}
	}
	return t
}

// SaveSettings writes every field, including the false/empty ones: a settings
// form that only persisted non-zero values would make a cleared store path or
// a switched-off web API impossible to save.
func (agentMemoryConfigStore) SaveSettings(ctx context.Context, id string, s agentmemory.Settings) error {
	if globalConfigs == nil {
		return nil
	}
	set := func(key, val string) error { return globalConfigs.SetOwned(ctx, "agents", key, val) }
	if err := set("agentmemory_data_dir", s.DataDir); err != nil {
		return err
	}
	if err := set("agentmemory_"+id+"_port", strconv.Itoa(s.Port)); err != nil {
		return err
	}
	if err := set("agentmemory_"+id+"_enable_web", boolStr(s.EnableWeb)); err != nil {
		return err
	}
	// Stored as submitted. The lock that forces it on while an instance uses
	// the backend is derived on read, never written — so switching the last
	// instance off restores whatever the operator had actually chosen.
	if err := set("agentmemory_"+id+"_autostart", boolStr(s.Autostart)); err != nil {
		return err
	}
	if err := set("agentmemory_backfill_max_sessions", strconv.Itoa(s.BackfillMaxSessions)); err != nil {
		return err
	}
	// The bearer token is the one secret in the block, so it is encrypted
	// before the row is written — the same treatment a provider instance's
	// key gets. An already-encrypted value passes through untouched.
	t := s.Tuning
	if tok := strings.TrimSpace(t.AuthToken); tok != "" && !strings.HasPrefix(tok, "wick_") {
		t.AuthToken = encryptSecretValue(tok)
	}
	blob, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return set("agentmemory_"+id+"_tuning", string(blob))
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// cfgBool reads an agents-owned config key as a bool. An absent row (or one
// holding anything else) means "never set", so it answers def — which is how
// the web API ships on: /api/v1 is where the panel's project list and search
// come from, and a default-off daemon would hand a fresh install an empty
// Projects tab and a dead search box (Yoga, 2026-09-25). SaveSettings always
// writes "true"/"false", so once the form is submitted the stored value wins.
func cfgBool(key string, def bool) bool {
	if globalConfigs == nil {
		return def
	}
	return parseCfgBool(globalConfigs.GetOwned("agents", key), def)
}

// parseCfgBool maps a stored config value onto a bool, with anything that is
// not an explicit "true"/"false" — an absent row reads as "" — meaning "never
// set" and answering def.
func parseCfgBool(v string, def bool) bool {
	switch v {
	case "true":
		return true
	case "false":
		return false
	default:
		return def
	}
}

// cfgInt reads an agents-owned config key as an int. A missing or unparsable
// value is 0, which every caller reads as "use the default".
func cfgInt(key string) int {
	if globalConfigs == nil {
		return 0
	}
	n, err := strconv.Atoi(globalConfigs.GetOwned("agents", key))
	if err != nil {
		return 0
	}
	return n
}

// AgentMemoryEnabled reports the master switch. Absent row = OFF, unlike AI
// Router's default-on: this feature spawns a daemon and writes a marker file
// into project folders, so a host that never asked for it gets neither. It is
// the same key server.go gates the marker writer on.
func AgentMemoryEnabled() bool {
	return globalConfigs != nil && globalConfigs.GetOwned("agents", "agentmemory_enabled") == "true"
}

// agentMemoryAdminOnly reports whether the request's user is an admin. The
// CONTROLS are admin-only — they start processes, rewrite the daemon's
// configuration and import history. Fail-closed when no user.
func agentMemoryAdminOnly(ctx context.Context) bool {
	u := login.GetUser(ctx)
	return u != nil && u.IsAdmin()
}

// agentMemoryViewer reports who may LOOK at the panel: anyone logged in
// (Yoga, PLAN §23.4). The store holds what agents saw in their sessions, so
// this is the one line to narrow if a wick install ever has users who should
// not see client data — the endpoints all read it through ConfigStore.
// Fail-closed when no user.
func agentMemoryViewer(ctx context.Context) bool {
	return login.GetUser(ctx) != nil
}

// AgentMemoryVisible reports whether the Agent Memory nav entry should show:
// master on AND the caller may at least look. It gates the project menu's
// entry too (PLAN §22.1) — the reading gate, not the managing one.
func AgentMemoryVisible(ctx context.Context) bool {
	return AgentMemoryEnabled() && agentMemoryViewer(ctx)
}

// AgentMemoryManageable reports whether the caller may drive the controls. The
// panel is handed this so it can leave the managing controls out entirely
// rather than render them dead — a disabled button only raises questions
// (PLAN §23.3).
func AgentMemoryManageable(ctx context.Context) bool {
	return AgentMemoryEnabled() && agentMemoryAdminOnly(ctx)
}

// AgentMemoryAutostart starts every backend whose autostart is effectively on
// at boot. Called from server.go after the tool router mounts, so the config
// store is wired.
func AgentMemoryAutostart(logf func(string)) { agentmemory.Autostart(logf) }

// AnyAgentMemoryAutostart reports whether at least one backend will start at
// boot — stored autostart, or forced by an instance that uses it.
func AnyAgentMemoryAutostart() bool { return agentmemory.AnyAutostartEnabled() }

// AgentMemoryStartWatchdog begins supervising the Agent Memory daemons: a
// dead one is started, a wedged one is restarted, and both are counted where
// the panel can show them. What it supervises is decided by the same
// effective-autostart signal the panel shows — not by a switch of its own
// (PLAN §25.1). Called from server.go at boot, after the routes mount so the
// config store behind that signal is wired.
func AgentMemoryStartWatchdog() { agentmemory.StartWatchdog() }

// AgentMemoryStopWatchdog ends supervision. Called before the daemons are
// stopped on shutdown, so supervision cannot race a stop by restarting what
// is being torn down.
func AgentMemoryStopWatchdog() { agentmemory.StopWatchdog() }

// SetAgentMemoryUpgradeWindow wires wick's graceful-handover signal into the
// watchdog. During a handover the successor adopts the running daemon and
// this process is about to exit, so supervision must stay quiet rather than
// fight a window it does not own (PLAN §25.3 guard 5).
func SetAgentMemoryUpgradeWindow(fn func() bool) { agentmemory.SetUpgradeWindow(fn) }

// AgentMemoryStopAll kills every Agent Memory daemon wick spawned. Called from
// the server's hard-stop path: the daemons are our children, and leaving one
// holding a loopback port after wick is gone makes the next boot look like
// someone else took the port. Not called on a graceful upgrade — the successor
// adopts the daemon that is already healthy instead of restarting it.
func AgentMemoryStopAll() { agentmemory.StopAll() }

// RegisterAgentMemory wires the Agent Memory daemon-control and panel-data
// endpoints plus the SPA page onto the agents tool router. Called from
// handler.go's Register.
func RegisterAgentMemory(r tool.Router) {
	agentmemory.RegisterRoutes(r, agentMemoryConfigStore{})
	r.GET("/agentmemory", agentMemoryPage)
}

// agentMemoryPage renders the Agent Memory SPA thin-shell inside the agents
// chrome. The SPA owns the backend switcher and the Overview/Projects/… tabs;
// this handler only supplies the layout, base, and the Vite bundle URL.
// FullBleed so the panel fills the content area. The gate matches the data
// endpoints exactly: 404 while the master switch is off (the feature looks
// absent rather than forbidden), 403 for anyone not logged in.
//
// CanManage rides along so the SPA knows which half of the panel to draw
// before its first fetch — without it a viewer would see the managing
// controls paint and then fail on 403.
func agentMemoryPage(c *tool.Ctx) {
	if !AgentMemoryEnabled() {
		c.Error(http.StatusNotFound, "agent memory disabled")
		return
	}
	if !agentMemoryViewer(c.Context()) {
		c.Error(http.StatusForbidden, "forbidden")
		return
	}
	layout := sidebarVM(c, "agentmemory", "")
	layout.FullBleed = true
	c.HTML(view.AgentMemoryPage(view.AgentMemoryVM{
		Layout:    layout,
		Base:      c.Base(),
		AssetURL:  spaAssetURL("agentmemory"),
		CanManage: AgentMemoryManageable(c.Context()),
	}))
}

// EnsureAgentMemoryConfigs declares every config row this feature writes.
//
// configs.SetOwned refuses a key it has no meta entry for, so an undeclared
// key is not a silent no-op — it fails the whole save. That is what the panel
// hit: pressing Save answered "unknown config agents/agentmemory_data_dir"
// and nothing in the Settings tab could be persisted at all.
//
// The per-backend rows are derived from the REGISTRY rather than listed by
// hand, which is the point: registering a second backend gives it its port,
// web, autostart and tuning rows for free. A hand-written list would have to
// be remembered, and the failure mode of forgetting is this same silent-until-
// you-press-Save break.
//
// Hidden, because these belong to the Agent Memory panel and not to the
// Settings page — the master switch is the one row an operator sets there.
// Hidden rows are still seeded, so runtime reads work normally.
func EnsureAgentMemoryConfigs(ctx context.Context, cfgs *configs.Service) error {
	if cfgs == nil {
		return nil
	}
	rows := []entity.Config{
		{Key: "agentmemory_data_dir", Type: "text", Hidden: true,
			Description: "Where the memory store lives on disk. Set from the Agent Memory panel."},
		{Key: "agentmemory_backfill_max_sessions", Type: "number", Hidden: true,
			Description: "Ceiling on how many local sessions one import may read. Set from the Agent Memory panel."},
	}
	for _, be := range agentmemory.List() {
		id := be.Desc.ID
		rows = append(rows,
			entity.Config{Key: "agentmemory_" + id + "_port", Type: "number", Hidden: true,
				Description: "Port for the " + be.Desc.DisplayName + " daemon. 0 = the backend's own default."},
			entity.Config{Key: "agentmemory_" + id + "_enable_web", Type: "bool", Hidden: true,
				Description: "Whether the " + be.Desc.DisplayName + " daemon serves its web API."},
			entity.Config{Key: "agentmemory_" + id + "_autostart", Type: "bool", Hidden: true,
				Description: "Start the " + be.Desc.DisplayName + " daemon when wick boots."},
			entity.Config{Key: "agentmemory_" + id + "_tuning", Type: "text", Hidden: true,
				Description: "Daemon tuning for " + be.Desc.DisplayName + ", as JSON. Applied as environment on launch."},
		)
	}
	return cfgs.EnsureOwned(ctx, "agents", rows...)
}
