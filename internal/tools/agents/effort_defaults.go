package agents

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/pkg/tool"
)

// A project's (and so its Team agent's) default reasoning effort lives in
// project.Defaults.Effort, next to the default provider and model it was
// chosen for. A chat that picked no effort runs with it — but only while
// the chat is on that same provider + model: a level chosen for one model
// means nothing on another, and a chat switched by hand keeps its model's
// own default instead of a stale level.

func init() {
	provider.EffortInherited = effortInheritedFor
}

// effortInheritedFor is provider.EffortInherited.
func effortInheritedFor(_ context.Context, sessionID string) (string, bool) {
	if globalMgr == nil {
		return "", false
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok || sess.Meta.ProjectID == "" {
		return "", false
	}
	proj, ok := globalMgr.Registry().Project(sess.Meta.ProjectID)
	if !ok {
		return "", false
	}
	d := proj.Meta.Defaults
	if strings.TrimSpace(d.Effort) == "" || strings.TrimSpace(d.Provider) == "" {
		return "", false
	}
	// agents.json from disk: the registry's copy can trail a switch.
	if globalLayout != (agentconfig.Layout{}) {
		if fresh, err := session.Load(globalLayout, sessionID); err == nil {
			sess = fresh
		}
	}
	_, a, ok := sessionAgentEntry(sess)
	if !ok || !sameDefaultPair(d, a.Provider, a.ModelID) {
		return "", false
	}
	return d.Effort, true
}

// sameDefaultPair reports whether an agent on prov + model runs the
// project's default pair. An empty default model means "the instance's
// own default", which an agent with no pin matches.
func sameDefaultPair(d project.Defaults, prov, model string) bool {
	if normalizeProviderKey(strings.TrimSpace(d.Provider)) != normalizeProviderKey(strings.TrimSpace(prov)) {
		return false
	}
	return strings.TrimSpace(d.Model) == strings.TrimSpace(model)
}

// nextDefaultEffort is the effort to store after a defaults write. want
// nil keeps the current effort while the provider + model pair (oldPair,
// "" = unknown/unchanged) stays and drops it when the pair changed. A
// given effort must be offered by the new pair; "default" stores as "".
func nextDefaultEffort(ctx context.Context, d project.Defaults, oldPair string, want *string) (string, error) {
	if want == nil {
		if oldPair != "" && oldPair != d.Provider+"\x00"+d.Model {
			return "", nil
		}
		return d.Effort, nil
	}
	v := strings.TrimSpace(*want)
	if v == "" || v == provider.EffortDefault {
		return "", nil
	}
	if strings.TrimSpace(d.Provider) == "" {
		return "", fmt.Errorf("pick a default provider before its reasoning effort")
	}
	info := provider.EffortInfoFor(ctx, d.Provider, d.Model, "", "")
	if info.Error != "" {
		return "", fmt.Errorf("reasoning effort: %s", info.Error)
	}
	if !provider.ValidEffortChoice(info, v) {
		return "", fmt.Errorf("effort %s is not offered by the selected model", v)
	}
	return v, nil
}

// getProviderEffort answers GET /providers/effort?provider=type/name&model=…
// with what that provider + model offers: the settings pages, the new
// session page and the failover editor use it to show the effort step
// right after a model is picked.
func getProviderEffort(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	prov := strings.TrimSpace(c.Query("provider"))
	if prov == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "provider required"})
		return
	}
	c.JSON(http.StatusOK, provider.EffortInfoFor(c.Context(), prov, strings.TrimSpace(c.Query("model")), "", ""))
}
