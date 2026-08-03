package agents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// AgentProfileItem is one sub-agent role as the admin UI sees it.
type AgentProfileItem struct {
	ID string `json:"id"`
	// ProjectID is the scope. Empty = global; non-empty = owned by that
	// project and invisible elsewhere.
	ProjectID          string   `json:"project_id"`
	Key                string   `json:"key"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Icon               string   `json:"icon"`
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	SystemPrompt       string   `json:"system_prompt"`
	AllowedTagIDs      []string `json:"allowed_tag_ids"`
	AllowedNativeTools []string `json:"allowed_native_tools"`
	StrictMCP          bool     `json:"strict_mcp"`
	DefaultMaxTurns    int      `json:"default_max_turns"`
	CanDelegate        bool     `json:"can_delegate"`
	AllowTakeOver      bool     `json:"allow_take_over"`
	Disabled           bool     `json:"disabled"`
	// Locked freezes the role: no edit, no delete, from any surface. Only
	// this UI can clear it — MCP may set it, never unset it.
	Locked bool `json:"locked"`
}

// leaderCapableProviders lists the providers that can act as a LEADER —
// i.e. that support MCP tool use and can therefore call wick_delegate.
// A provider missing here can still be a sub-agent; it just cannot
// delegate onward, so can_delegate is forced off on save rather than
// silently accepted and then failing at runtime.
var leaderCapableProviders = map[string]bool{
	"claude": true,
	"codex":  true,
	"wick":   true,
}

func profileToItem(p entity.AgentProfile) AgentProfileItem {
	return AgentProfileItem{
		ID: p.ID, ProjectID: p.ProjectID, Key: p.Key, Name: p.Name, Description: p.Description,
		Icon: p.Icon, Provider: p.Provider, Model: p.Model,
		SystemPrompt:       p.SystemPrompt,
		AllowedTagIDs:      decodeJSONStrings(p.AllowedTagIDs),
		AllowedNativeTools: decodeJSONStrings(p.AllowedNativeTools),
		StrictMCP:          p.StrictMCP,
		DefaultMaxTurns:    p.DefaultMaxTurns,
		CanDelegate:        p.CanDelegate,
		AllowTakeOver:      p.AllowTakeOver,
		Disabled:           p.Disabled,
		Locked:             p.Locked,
	}
}

func decodeJSONStrings(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	return out
}

func encodeJSONStrings(in []string) string {
	if len(in) == 0 {
		return "[]"
	}
	b, err := json.Marshal(in)
	if err != nil {
		return "[]"
	}
	return string(b)
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// requireProfileAdmin gates admin-only mutations in this area (boards).
// Profiles no longer use it directly: their scope decides the authority,
// so they route through requireProfileScope instead.
func requireProfileAdmin(c *tool.Ctx) bool {
	u := login.GetUser(c.Context())
	if u == nil || !u.IsAdmin() {
		c.JSON(http.StatusForbidden, map[string]string{"error": "admin only"})
		return false
	}
	return true
}

// canMutateProfileScope reports whether a caller may create, edit, or
// delete a role in the given scope.
//
// The two scopes answer to different authorities, and neither implies the
// other: a global role is reachable from every project, so it stays
// admin-only, while a project role is confined to people who already hold
// the project. Kept as a pure function because both the save and delete
// paths must apply exactly the same rule — written twice, they drift.
func canMutateProfileScope(projectID string, isAdmin bool, allowProject func(string) bool) bool {
	if projectID == "" {
		return isAdmin
	}
	return allowProject(projectID)
}

// requireProfileScope enforces canMutateProfileScope and writes the right
// refusal. Global scope reports 403 "admin only" (the resource exists,
// you may not touch it); an inaccessible project reports 403 too, since
// the caller already named the project id in their own request.
func requireProfileScope(c *tool.Ctx, projectID string) bool {
	u := login.GetUser(c.Context())
	isAdmin := u != nil && u.IsAdmin()
	if canMutateProfileScope(projectID, isAdmin, callerProjectAccess(c).allowProject) {
		return true
	}
	if projectID == "" {
		c.JSON(http.StatusForbidden, map[string]string{"error": "admin only"})
		return false
	}
	c.JSON(http.StatusForbidden, map[string]string{"error": "no access to that project"})
	return false
}

func delegationReady(c *tool.Ctx) bool {
	if globalDelegation == nil || globalDelegation.Repo == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sub-agents are not enabled"})
		return false
	}
	return true
}

// tagOption is one selectable tag in the profile editor.
type tagOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// callerTagOptions returns the tags the caller actually holds.
//
// Deliberately not "every tag in the system": allowed_tag_ids can only
// narrow the delegating human's own set, so offering a tag the caller
// does not hold would be offering a choice with no effect.
func callerTagOptions(c *tool.Ctx) []tagOption {
	out := make([]tagOption, 0)
	if globalTagsSvc == nil {
		return out
	}
	ids := login.GetUserTagIDs(c.Context())
	if len(ids) == 0 {
		return out
	}
	tags, err := globalTagsSvc.TagsByIDs(c.Context(), ids)
	if err != nil {
		return out
	}
	for _, t := range tags {
		if t == nil {
			continue
		}
		out = append(out, tagOption{ID: t.ID, Name: t.Name})
	}
	return out
}

// leaderProviderOptions lists the providers a role may run on, healthy
// instances first and falling back to the leader-capable set when the
// provider cache is cold — an empty dropdown would make the form look
// broken rather than merely uninformed.
func leaderProviderOptions(c *tool.Ctx) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, ch := range providerChoicesCached(c.Context()) {
		if ch.Type == "" || seen[ch.Type] {
			continue
		}
		seen[ch.Type] = true
		out = append(out, ch.Type)
	}
	if len(out) == 0 {
		for _, p := range []string{"claude", "codex", "wick"} {
			out = append(out, p)
		}
	}
	return out
}

// apiAgentProfileList handles GET /api/agent-profiles.
//
// Admins see every profile so they can manage them. Everyone else sees
// only the profiles they could actually delegate to, using the same
// visibility rule the MCP roster applies — one rule, two surfaces.
func apiAgentProfileList(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	u := login.GetUser(c.Context())
	isAdmin := u != nil && u.IsAdmin()

	// An absent project_id means the global scope, which is what the
	// standalone Sub-agents page shows. The project tab passes its own id
	// and gets the globals plus its own roles, shadows applied.
	projectID := strings.TrimSpace(c.Query("project_id"))
	if projectID != "" && !callerProjectAccess(c).allowProject(projectID) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}

	rows, err := globalDelegation.Repo.ListProfilesInScopes(c.Context(), projectID, isAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	profiles := delegation.ResolveScoped(rows, projectID)
	if !isAdmin {
		profiles = delegation.VisibleProfiles(profiles, login.GetUserTagIDs(c.Context()), false)
	}

	items := make([]AgentProfileItem, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, profileToItem(p))
	}

	// The project tab needs the UNRESOLVED split, which the merged list
	// cannot express: once a project row shadows a global one, the global
	// row is gone from `profiles` and nothing left in the payload records
	// that it was ever there. So both halves are returned as they are
	// stored — "owned" is what this project may edit, "inherited" is what
	// it received from the global scope.
	owned := make([]AgentProfileItem, 0)
	inherited := make([]AgentProfileItem, 0)
	if projectID != "" {
		for _, p := range rows {
			if p.ProjectID == projectID {
				owned = append(owned, profileToItem(p))
			} else {
				inherited = append(inherited, profileToItem(p))
			}
		}
	}
	// Options ride along on the list rather than getting their own route:
	// the editor cannot render without them, so a second fetch would only
	// add a state where the form is up but not yet usable.
	//
	// The tag choices are the CALLER's own tags. That is not a shortcut —
	// allowed_tag_ids only ever narrows the delegating human's set, so a
	// tag the caller does not hold would be inert if offered.
	c.JSON(http.StatusOK, map[string]any{
		"profiles":  items,
		"owned":     owned,
		"inherited": inherited,
		"tags":      callerTagOptions(c),
		"providers": leaderProviderOptions(c),
		"is_admin":  isAdmin,
	})
}

// lockGuardSave decides what a save may do to a role that is already
// locked.
//
// unlockOnly=true means "keep every stored field and flip Locked to
// false". Unlock and edit are deliberately two separate saves: if one
// request could do both, the lock would stop being a guard and become a
// single extra click on the way to the same change.
func lockGuardSave(existing *entity.AgentProfile, reqLocked bool) (unlockOnly bool, err error) {
	if existing == nil || !existing.Locked {
		return false, nil
	}
	if reqLocked {
		return false, delegation.CheckMutable(existing)
	}
	return true, nil
}

// apiAgentProfileSave handles POST /api/agent-profiles — create or update.
func apiAgentProfileSave(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	var req AgentProfileItem
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<20)).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}

	// The permission check runs after decoding because the scope arrives in
	// the body. A global role is reachable from every project, so editing
	// one stays admin-only; a project role is confined to people who
	// already hold the project, so project access is the bar.
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	if !requireProfileScope(c, req.ProjectID) {
		return
	}

	req.Key = strings.TrimSpace(req.Key)
	req.Provider = strings.TrimSpace(req.Provider)
	if req.Key == "" || req.Provider == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "key and provider are required"})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = req.Key
	}
	// A profile with no description is close to unusable: the description
	// is what the leader model reads to decide whether to delegate here.
	if strings.TrimSpace(req.Description) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "description is required — the delegating agent uses it to decide when to pick this role",
		})
		return
	}
	// Force can_delegate off for providers that cannot use MCP tools; a
	// stored true would just fail at runtime.
	if !leaderCapableProviders[req.Provider] {
		req.CanDelegate = false
	}

	// Scope-exact, never resolved: a resolved lookup would find the GLOBAL
	// row when a project creates a role of the same name, and overwrite it
	// — silently editing the role every other project sees.
	existing, err := globalDelegation.Repo.GetProfileExact(c.Context(), req.ProjectID, req.Key)
	if err != nil && !errors.Is(err, delegation.ErrProfileNotFound) {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	unlockOnly, lerr := lockGuardSave(existing, req.Locked)
	if lerr != nil {
		c.JSON(http.StatusConflict, map[string]string{"error": lerr.Error()})
		return
	}
	// An unlock is applied to the STORED row, not to the submitted one:
	// the form disables every other control while locked, but a hand-made
	// request must not be able to ride along on the unlock.
	if unlockOnly {
		row := *existing
		row.Locked = false
		if err := globalDelegation.Repo.SaveProfile(c.Context(), &row); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, profileToItem(row))
		return
	}

	p := &entity.AgentProfile{
		ID: req.ID, ProjectID: req.ProjectID,
		Key: req.Key, Name: req.Name, Description: req.Description,
		Icon: req.Icon, Provider: req.Provider, Model: req.Model,
		SystemPrompt:       req.SystemPrompt,
		AllowedTagIDs:      encodeJSONStrings(req.AllowedTagIDs),
		AllowedNativeTools: encodeJSONStrings(req.AllowedNativeTools),
		StrictMCP:          req.StrictMCP,
		DefaultMaxTurns:    req.DefaultMaxTurns,
		CanDelegate:        req.CanDelegate,
		AllowTakeOver:      req.AllowTakeOver,
		Disabled:           req.Disabled,
		Locked:             req.Locked,
	}
	if existing != nil {
		p.ID = existing.ID
		p.CreatedBy = existing.CreatedBy
		p.CreatedAt = existing.CreatedAt
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.CreatedBy == "" {
		if u := login.GetUser(c.Context()); u != nil {
			p.CreatedBy = u.ID
		}
	}
	if p.DefaultMaxTurns <= 0 {
		p.DefaultMaxTurns = delegation.DefaultMaxTurns
	}

	if err := globalDelegation.Repo.SaveProfile(c.Context(), p); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Mint the per-profile filter tag so an admin can grant delegation
	// rights to this role without also granting every other role.
	ensureProfileTag(c, p.Key)
	c.JSON(http.StatusOK, profileToItem(*p))
}

// apiAgentProfileDelete handles DELETE /api/agent-profiles/{id}.
func apiAgentProfileDelete(c *tool.Ctx) {
	if notReady(c) || !delegationReady(c) {
		return
	}
	id := c.PathValue("id")
	existing, err := globalDelegation.Repo.GetProfileByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, delegation.ErrProfileNotFound) {
			c.JSON(http.StatusNotFound, map[string]string{"error": "profile not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The row's own scope decides who may remove it — the same rule the
	// save path applies, via the same function.
	if !requireProfileScope(c, existing.ProjectID) {
		return
	}
	// Locked blocks delete too. If it did not, the lock would guard
	// nothing: the role could be removed and recreated under the same key
	// with different behaviour.
	if err := delegation.CheckMutable(existing); err != nil {
		c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := globalDelegation.Repo.DeleteProfile(c.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

// ensureProfileTag creates the "agent:<key>" filter tag for a profile,
// mirroring the connector owner-tag convention. Best-effort: a tagging
// failure must not undo a saved profile, so it is logged by the tags
// service and ignored here.
func ensureProfileTag(c *tool.Ctx, key string) {
	if globalTagsSvc == nil || key == "" {
		return
	}
	u := login.GetUser(c.Context())
	if u == nil {
		return
	}
	_ = globalTagsSvc.CreateResourceOwnerTag(c.Context(), "agent:"+key, u.ID)
}
