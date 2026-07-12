package agents

// AI-router glue that needs the agents package's globals. The feature itself
// lives in the self-contained internal/agents/airouter package (+ one folder
// per router under it); this file backs its ConfigStore with the app config
// service, renders the SPA shell, and exposes the boot/mount helpers server.go
// wires. The blank imports register the built-in routers (9router, OmniRoute).

import (
	"context"
	"net/http"

	"github.com/yogasw/wick/internal/agents/airouter"
	_ "github.com/yogasw/wick/internal/agents/airouter/omniroute"
	_ "github.com/yogasw/wick/internal/agents/airouter/router9"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// airouterConfigStore implements airouter.ConfigStore over globalConfigs.
// Per-router knobs are stored under agents.airouter_<id>_autostart /
// _external; the master switch is agents.airouter_enabled. Legacy 9router keys
// are read as a fallback so an existing setup keeps its settings.
type airouterConfigStore struct{}

func (airouterConfigStore) Enabled() bool { return AirouterEnabled() }

func (airouterConfigStore) AccessAllowed(ctx context.Context) bool {
	return airouterAdminOnly(ctx)
}

func (airouterConfigStore) GetAutostart(id string) bool { return airouterFlag(id, "autostart") }

func (airouterConfigStore) SetAutostart(ctx context.Context, id string, on bool) error {
	return setAirouterFlag(ctx, id, "autostart", on)
}

func (airouterConfigStore) GetExternalAPI(id string) bool { return airouterFlag(id, "external") }

func (airouterConfigStore) SetExternalAPI(ctx context.Context, id string, on bool) error {
	return setAirouterFlag(ctx, id, "external", on)
}

// ExternalAPIAllowed reports whether off-machine callers may reach router id's
// /v1 API. Gated by the master switch too.
func (airouterConfigStore) ExternalAPIAllowed(id string) bool {
	return AirouterEnabled() && airouterFlag(id, "external")
}

// airouterFlag reads a per-router boolean knob (kind = "autostart"|"external"),
// falling back to the legacy 9router config keys for the 9router id so an
// existing install keeps its settings after the rename.
func airouterFlag(id, kind string) bool {
	if globalConfigs == nil {
		return false
	}
	v := globalConfigs.GetOwned("agents", "airouter_"+id+"_"+kind)
	if v == "" && id == "9router" {
		switch kind {
		case "autostart":
			v = globalConfigs.GetOwned("agents", "router9autostart")
		case "external":
			v = globalConfigs.GetOwned("agents", "router9external_api")
		}
	}
	return v == "true"
}

func setAirouterFlag(ctx context.Context, id, kind string, on bool) error {
	val := "false"
	if on {
		val = "true"
	}
	return globalConfigs.SetOwned(ctx, "agents", "airouter_"+id+"_"+kind, val)
}

// AirouterEnabled reports the master switch. Absent row = ON (default true,
// matching DefaultGeneralConfig) so an existing setup keeps working until an
// admin disables it. Falls back to the legacy router9enabled key.
func AirouterEnabled() bool {
	if globalConfigs == nil {
		return true
	}
	v := globalConfigs.GetOwned("agents", "airouter_enabled")
	if v == "" {
		v = globalConfigs.GetOwned("agents", "router9enabled")
	}
	return v == "" || v == "true"
}

// airouterAdminOnly reports whether the request's user is an admin. The AI
// Router dashboard + controls are admin-only. Fail-closed when no user.
func airouterAdminOnly(ctx context.Context) bool {
	u := login.GetUser(ctx)
	return u != nil && u.IsAdmin()
}

// AirouterVisible reports whether the AI Router nav entry should show: master
// on AND the caller has access.
func AirouterVisible(ctx context.Context) bool {
	return AirouterEnabled() && airouterAdminOnly(ctx)
}

// AirouterExternalAllowed reports whether router id's /v1 API may be reached
// off-machine — the host-allowlist exemption in server.go consults this for a
// non-loopback caller on the /airouter/<id>/v1 subtree.
func AirouterExternalAllowed(id string) bool {
	return airouterConfigStore{}.ExternalAPIAllowed(id)
}

// AirouterAutostart starts every auto-start-enabled router at boot. Called
// from server.go after the tool router mounts (so the config store is wired).
func AirouterAutostart(logf func(string)) { airouter.Autostart(logf) }

// AnyAirouterAutostart reports whether at least one router has auto-start on.
func AnyAirouterAutostart() bool { return airouter.AnyAutostartEnabled() }

// RegisterAirouter wires the AI Router control endpoints + SPA page onto the
// agents tool router. Called from handler.go's Register.
func RegisterAirouter(r tool.Router) {
	airouter.RegisterRoutes(r, airouterConfigStore{})
	r.GET("/airouter", airouterPage)
}

// airouterPage renders the AI Router SPA thin-shell inside the agents chrome.
// The SPA owns the router switcher + per-router Dashboard/Requests/Settings
// tabs; this handler only supplies the layout, base, and the Vite bundle URL.
// FullBleed so the iframe/stream fills the content area. 404 when the master
// switch is off; 403 for non-admins.
func airouterPage(c *tool.Ctx) {
	if !AirouterEnabled() {
		c.Error(http.StatusNotFound, "airouter disabled")
		return
	}
	if !requireAdmin(c) {
		return
	}
	layout := sidebarVM(c, "airouter", "")
	layout.FullBleed = true
	c.HTML(view.AirouterPage(view.AirouterVM{
		Layout:   layout,
		Base:     c.Base(),
		AssetURL: spaAssetURL("airouter"),
	}))
}
