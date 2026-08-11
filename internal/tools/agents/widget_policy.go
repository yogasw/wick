package agents

import (
	agentsconfig "github.com/yogasw/wick/internal/agents/config"
)

// Widget CSP resolution for the conversation SPA.
//
// The SPA never sees the global-vs-project split: it receives one already
// resolved policy and builds the iframe CSP from it. Resolution happens
// here so the ordering rules (project override is wholesale, allowlist
// appends) live in exactly one place — agentsconfig.Resolve.

// globalWidgetPolicy reads the six Widget knobs out of the configs table.
//
// Every directive fails closed: a missing row, a cleared value, or an
// unrecognised string all resolve to "block" inside
// agentsconfig.Resolve, so an install that has never touched these
// settings behaves exactly like the hardcoded policy that preceded them.
// AllowPopups is read with the == "true" polarity for the same reason.
func globalWidgetPolicy() agentsconfig.WidgetPolicy {
	if globalConfigs == nil {
		return agentsconfig.WidgetPolicy{}
	}
	get := func(key string) string { return globalConfigs.GetOwned("agents", key) }
	return agentsconfig.WidgetPolicy{
		Mode:        get("widget_mode"),
		FrameSrc:    get("widget_frame_src"),
		ImgSrc:      get("widget_img_src"),
		MediaSrc:    get("widget_media_src"),
		ConnectSrc:  get("widget_connect_src"),
		ScriptSrc:   get("widget_script_src"),
		AllowPopups: get("widget_allow_popups") == "true",
		Allowlist:   agentsconfig.ParseAllowlist(get("widget_allowlist")),
	}
}

// resolveWidgetPolicy returns the effective policy for a session's
// project. An empty projectID, or a project the registry does not know,
// yields the global policy — a session outside any project has no
// override to apply.
func resolveWidgetPolicy(projectID string) agentsconfig.WidgetPolicy {
	global := globalWidgetPolicy()
	if projectID == "" || globalMgr == nil {
		return agentsconfig.Resolve(global, agentsconfig.WidgetPolicy{})
	}
	p, ok := globalMgr.Registry().Project(projectID)
	if !ok {
		return agentsconfig.Resolve(global, agentsconfig.WidgetPolicy{})
	}
	return agentsconfig.Resolve(global, p.Meta.Widget)
}
