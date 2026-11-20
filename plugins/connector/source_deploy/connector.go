package main

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

const Key = "source_deploy"

// Module returns the connector, its runs and profiles under the plugin's own data dir: it
// outlives a plugin update, so a newer build of this plugin picks up the runs of the old.
func Module() connector.Module {
	return moduleFor(New(wickplugin.DataDir(Key)))
}

func moduleFor(svc *Service) connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:  Key,
			Name: "Deploy from Source",
			Description: "Update THIS wick from source — a branch (develop, a feature branch), a fork or a local checkout — " +
				"without a release. Runs the deploy-from-source script (.sh on Linux/macOS/Termux, .ps1 on Windows) in the " +
				"background, outside wick, so it survives the reload it performs. Save a profile once, then deploy with no input.",
			Icon: "🚀",
			// System: only admins see it — what it runs is code on this machine.
			DefaultTags: []entity.DefaultTag{tags.Connector, tags.System},
		},
		Operations: Operations(svc),
	}
}

type saveInput struct {
	Name    string `wick:"key=name;required;desc=Profile name, e.g. wick-develop."`
	Script  string `wick:"key=script;desc=https URL or local path of the deploy script. Empty = wick's own scripts/deploy-from-source (.sh, or .ps1 on Windows) from master."`
	Args    string `wick:"key=args;textarea;desc=Script arguments: a JSON array ([\"--ref\",\"develop\"]) or space-separated (--ref develop). Run the script with --help for the list: wick (default) builds wick itself at --ref, core/plugins/all build your own wick app."`
	Default bool   `wick:"key=default;desc=Make this the profile deploy uses when no profile is named. The first profile saved is the default anyway."`
}

type nameInput struct {
	Name string `wick:"key=name;required;desc=Profile name."`
}

type deployInput struct {
	Profile string `wick:"key=profile;desc=Profile to run. Empty = the default profile (or, with none saved, wick from master)."`
	Args    string `wick:"key=args;desc=Extra arguments for this run only, appended after the profile's, e.g. --force or --dry-run."`
}

type runInput struct {
	RunID string `wick:"key=run_id;desc=Run id from deploy. Empty = the newest run."`
	Lines int    `wick:"key=lines;number;desc=Log lines to return (default 20, max 200)."`
}

type cancelInput struct {
	RunID string `wick:"key=run_id;desc=Run id. Empty = the newest run."`
}

type listRunsInput struct {
	Limit int `wick:"key=limit;number;desc=How many runs, newest first (default 10)."`
}

func Operations(svc *Service) []connector.Category {
	h := handlers{svc: svc}
	return []connector.Category{
		connector.Cat("Profiles", "Saved ways to deploy: which script, which arguments.",
			connector.Op("list_profiles", "List Profiles",
				"List the saved deploy profiles, the default first. Returns {profiles:[{name,script,args,default}]}.",
				struct{}{}, h.listProfiles, wickdocs.Docs{}),
			connector.OpDestructive("save_profile", "Save Profile",
				"Create or replace a deploy profile. A profile decides what code this machine builds and runs, "+
					"so save it once, deliberately; deploy afterwards needs no input. Example: name=wick-develop, args=--ref develop.",
				saveInput{}, h.saveProfile, wickdocs.Docs{}),
			connector.OpDestructive("delete_profile", "Delete Profile",
				"Delete a deploy profile. Deleting the default makes the next one the default.",
				nameInput{}, h.deleteProfile, wickdocs.Docs{}),
		),
		connector.Cat("Runs", "Start a deploy and follow it.",
			connector.Op("deploy", "Deploy",
				"Build and install from source using a profile. Returns at once with {run_id, state:running}: "+
					"the build runs in the background, outside wick, and survives the reload it ends with. "+
					"One run at a time. Follow it with status.",
				deployInput{}, h.deploy, wickdocs.Docs{}),
			connector.Op("status", "Run Status",
				"State of a run (running/succeeded/failed/canceled), its exit code, the current stage "+
					"(the script's last \"== …\" line) and the tail of its log.",
				runInput{}, h.status, wickdocs.Docs{}),
			connector.Op("list_runs", "List Runs", "Recent runs, newest first.",
				listRunsInput{}, h.listRuns, wickdocs.Docs{}),
			connector.Op("cancel", "Cancel Run",
				"Stop a running deploy and everything it started. A cancel during the binary swap is safe: "+
					"the reload it runs rolls back on its own.",
				cancelInput{}, h.cancel, wickdocs.Docs{}),
		),
	}
}

type handlers struct {
	svc *Service
}

func (h handlers) listProfiles(c *connector.Ctx) (any, error) {
	ps, err := h.svc.Profiles()
	if err != nil {
		return nil, err
	}
	if ps == nil {
		ps = []Profile{}
	}
	res := map[string]any{"profiles": ps}
	if len(ps) == 0 {
		res["note"] = "no profile saved: deploy with no profile builds wick from master with the default script"
	}
	return res, nil
}

func (h handlers) saveProfile(c *connector.Ctx) (any, error) {
	args, err := ParseArgs(c.Input("args"))
	if err != nil {
		return nil, err
	}
	p, err := h.svc.SaveProfile(Profile{
		Name:      c.Input("name"),
		Script:    c.Input("script"),
		Args:      args,
		Default:   c.InputBool("default"),
		UpdatedBy: c.CallerUserID(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "profile": p}, nil
}

func (h handlers) deleteProfile(c *connector.Ctx) (any, error) {
	if err := h.svc.DeleteProfile(strings.TrimSpace(c.Input("name"))); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (h handlers) deploy(c *connector.Ctx) (any, error) {
	extra, err := ParseArgs(c.Input("args"))
	if err != nil {
		return nil, err
	}
	r, err := h.svc.Start(c.Context(), StartOptions{
		Profile:   c.Input("profile"),
		ExtraArgs: extra,
		StartedBy: c.CallerUserID(),
		SessionID: c.SessionID(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"run_id":  r.ID,
		"state":   r.State,
		"profile": r.Profile,
		"script":  r.Script,
		"args":    r.Args,
		"note":    "running in the background, outside wick: completion is published directly on source_deploy:run/<run_id>; status (run_id) remains available for details.",
	}, nil
}

func (h handlers) status(c *connector.Ctx) (any, error) {
	n := c.InputInt("lines")
	if n > 200 {
		n = 200
	}
	return h.svc.Get(c.Input("run_id"), n)
}

func (h handlers) listRuns(c *connector.Ctx) (any, error) {
	n := c.InputInt("limit")
	if n <= 0 {
		n = 10
	}
	rs, err := h.svc.Runs(n)
	if err != nil {
		return nil, err
	}
	if rs == nil {
		rs = []Run{}
	}
	return map[string]any{"runs": rs}, nil
}

func (h handlers) cancel(c *connector.Ctx) (any, error) {
	r, err := h.svc.Cancel(c.Input("run_id"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "run": r}, nil
}

// ParseArgs reads arguments as a JSON array or space-separated words. Empty is no args.
func ParseArgs(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, "[") {
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, errors.New("args: not a JSON array of strings")
		}
		return out, nil
	}
	return strings.Fields(s), nil
}
