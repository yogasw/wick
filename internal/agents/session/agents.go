package session

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// AgentEntry is one row in sessions/<id>/agents.json. CLISessionID is
// the resume key — wick captures it from the first stream-json event
// emitted by the CLI and persists it so subsequent spawns can pass
// `--resume <id>`. See agents-design.md §5.2.
//
// ProviderSessions maps a provider TYPE ("claude", "codex") to that
// type's last-known CLI session ID. Each type mints and owns its own
// ids — the transcript stores are not interchangeable — so the outgoing
// id is parked here on every switch and read back when the session
// returns to that type. Keyed by type, not instance: two claude
// instances read the same transcript store, so switching between them
// must keep the id. See ProviderScope / ResumeIDForScope.
type AgentEntry struct {
	Name             string            `json:"name"`
	Provider         string            `json:"provider"`
	CLISessionID     string            `json:"cli_session_id,omitempty"`
	Status           string            `json:"status"`
	CreatedAt        time.Time         `json:"created_at"`
	LastActive       time.Time         `json:"last_active,omitempty"`
	ProviderSessions map[string]string `json:"provider_sessions,omitempty"`
	// MaxTurns caps agentic turns on the next spawn (--max-turns).
	// 0 = unlimited (provider default). Set per workflow agent node.
	MaxTurns int `json:"max_turns,omitempty"`
	// ThinkingTokens is the resolved MAX_THINKING_TOKENS env value applied on
	// the next spawn (claude). Empty = unset (provider default, thinking on);
	// "0" = disabled; "<n>" = budget. Set per workflow agent node from its
	// thinking + max_thinking_tokens inputs.
	ThinkingTokens string `json:"thinking_tokens,omitempty"`
	// ModelID pins a specific model id this agent uses on its next spawn,
	// scoped to whichever provider Provider currently points at. Empty =
	// use that provider instance's own default-model resolution. Currently
	// only meaningful for wick (WickModel.ID); other provider types ignore
	// it. Cleared on switching to a provider type that doesn't recognize
	// it, so a later switch back doesn't resurrect a stale/foreign pin.
	ModelID string `json:"model_id,omitempty"`
}

// ProviderScope returns the provider type of a provider key
// ("claude/work" → "claude").
//
// Every instance of one provider type resumes from the same transcript
// store — the same CLI binary reading the same on-disk history — so a
// resume id captured under one instance is valid under any other of that
// type, and never valid under a different type. ProviderSessions is
// keyed by this.
//
// Lives here rather than in the provider package because the switcher
// AND the store both write ProviderSessions and must agree on the key;
// provider imports store, so store cannot import provider, and session
// is the package both already depend on.
func ProviderScope(key string) string {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i]
	}
	return key
}

// ResumeIDForScope finds the resume id recorded for a provider type,
// tolerating every key shape written before ids were scoped: the bare
// scope, the normalized "type/type", and any "type/<instance>". Keys are
// sorted so a file holding several legacy instance keys of one type
// resolves the same id on every call — Go randomizes map iteration, and
// an id that changes per lookup is a resume that works only sometimes.
func ResumeIDForScope(m map[string]string, scope string) string {
	if id := m[scope]; id != "" {
		return id
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v != "" && ProviderScope(k) == scope {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return m[keys[0]]
}

// SaveAgents atomically rewrites sessions/<id>/agents.json. nil
// becomes an empty array on disk so consumers don't have to handle
// `null`.
func SaveAgents(layout config.Layout, id string, agents []AgentEntry) error {
	if err := storage.ValidateSessionID(id); err != nil {
		return err
	}
	if !storage.PathExists(layout.SessionDir(id)) {
		return fmt.Errorf("session %q not found", id)
	}
	if agents == nil {
		agents = []AgentEntry{}
	}
	return storage.WriteJSON(layout.SessionAgents(id), agents)
}

// ErrNoAgentName rejects a blank agent name.
//
// The setters below create an entry when the name does not match, which is
// how a single blank name turned into a growing list of unaddressable rows:
// nothing can ever match "" again, so every later call appended another one,
// and the session appeared to hold a second agent nobody created. A blank
// name is always a caller bug, so it fails loudly instead of writing.
var ErrNoAgentName = errors.New("session: agent name is empty")

// AddAgent appends a new agent entry. Errors on duplicate name within
// the same session.
func AddAgent(layout config.Layout, id, name, provider string) error {
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	for _, a := range sess.Agents {
		if a.Name == name {
			return fmt.Errorf("agent %q already exists in session %q", name, id)
		}
	}
	sess.Agents = append(sess.Agents, AgentEntry{
		Name:      name,
		Provider:  provider,
		Status:    "idle",
		CreatedAt: time.Now().UTC(),
	})
	return SaveAgents(layout, id, sess.Agents)
}

// SetAgentProvider repoints an existing agent entry at another provider
// instance, leaving the rest of the entry (resume id, caps) intact.
//
// Distinct from AddAgent, which refuses a name that already exists. Used
// to repair an entry created with no provider: the spawn path has to fill
// one in, and re-adding would drop the resume id the entry already
// carries. No-op when the entry doesn't exist.
func SetAgentProvider(layout config.Layout, id, name, providerKey string) error {
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	for i := range sess.Agents {
		if sess.Agents[i].Name == name {
			sess.Agents[i].Provider = providerKey
			return SaveAgents(layout, id, sess.Agents)
		}
	}
	return nil
}

// SetModelIDIfEmpty pins a model only when the entry carries no pin.
//
// Inherited defaults use this rather than SetModelID: a session already
// pinned to a model chose that deliberately, and a project default must
// not silently move it. No-op when the entry doesn't exist or already has
// a pin.
func SetModelIDIfEmpty(layout config.Layout, id, name, modelID string) error {
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	for i := range sess.Agents {
		if sess.Agents[i].Name != name {
			continue
		}
		if sess.Agents[i].ModelID != "" {
			return nil
		}
		sess.Agents[i].ModelID = modelID
		return SaveAgents(layout, id, sess.Agents)
	}
	return nil
}

// SetCLISessionID writes (or clears, with "") the CLI resume id on the
// agent entry. No-op when the entry doesn't exist.
func SetCLISessionID(layout config.Layout, id, name, cliID string) error {
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	for i := range sess.Agents {
		if sess.Agents[i].Name == name {
			sess.Agents[i].CLISessionID = cliID
			return SaveAgents(layout, id, sess.Agents)
		}
	}
	return nil
}

// SetMaxTurns persists the per-spawn turn cap on the agent entry,
// creating it if missing. 0 = unlimited (provider default).
func SetMaxTurns(layout config.Layout, id, name string, maxTurns int) error {
	if name == "" {
		return ErrNoAgentName
	}
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	for i := range sess.Agents {
		if sess.Agents[i].Name == name {
			sess.Agents[i].MaxTurns = maxTurns
			return SaveAgents(layout, id, sess.Agents)
		}
	}
	sess.Agents = append(sess.Agents, AgentEntry{
		Name:      name,
		Status:    "idle",
		CreatedAt: time.Now().UTC(),
		MaxTurns:  maxTurns,
	})
	return SaveAgents(layout, id, sess.Agents)
}

// SetThinkingTokens persists the resolved MAX_THINKING_TOKENS env value on
// the agent entry, creating it if missing. Empty = unset (provider default,
// thinking on); "0" = disabled; "<n>" = budget. Always persisted (including
// "") so switching a reused session back to full thinking clears a prior value.
func SetThinkingTokens(layout config.Layout, id, name, v string) error {
	if name == "" {
		return ErrNoAgentName
	}
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	for i := range sess.Agents {
		if sess.Agents[i].Name == name {
			sess.Agents[i].ThinkingTokens = v
			return SaveAgents(layout, id, sess.Agents)
		}
	}
	sess.Agents = append(sess.Agents, AgentEntry{
		Name:           name,
		Status:         "idle",
		CreatedAt:      time.Now().UTC(),
		ThinkingTokens: v,
	})
	return SaveAgents(layout, id, sess.Agents)
}

// SetModelID persists the pinned model id on the agent entry, creating it
// if missing. Empty = unset (the active provider's own default applies).
func SetModelID(layout config.Layout, id, name, modelID string) error {
	if name == "" {
		return ErrNoAgentName
	}
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	for i := range sess.Agents {
		if sess.Agents[i].Name == name {
			sess.Agents[i].ModelID = modelID
			return SaveAgents(layout, id, sess.Agents)
		}
	}
	sess.Agents = append(sess.Agents, AgentEntry{
		Name:      name,
		Status:    "idle",
		CreatedAt: time.Now().UTC(),
		ModelID:   modelID,
	})
	return SaveAgents(layout, id, sess.Agents)
}

// ActiveRunTarget reports the provider instance + model a session is
// actually running on, read from its active agent entry.
//
// This is the pair a delegated sub-agent inherits when its role names no
// provider of its own, so it must read the SAME fields the pool reads on
// spawn — Provider and ModelID off ONE entry — rather than reconstructing
// the choice from elsewhere. Returned as two strings rather than a
// provider.RunTarget because provider imports this package; the caller
// pairs them.
//
// The entry is picked by Meta.ActiveAgent, falling back to the first one:
// a session created outside the UI flow may never have had an active
// agent set, and treating that as "runs on nothing" would silently drop
// the inheritance the caller asked for.
//
// ok=false means the session could not be read or has no agent entries —
// distinct from an entry that names no provider, which is a real answer
// ("this session has no opinion") and returns ok=true with an empty
// providerKey.
func ActiveRunTarget(layout config.Layout, id string) (providerKey, modelID string, ok bool) {
	sess, err := Load(layout, id)
	if err != nil {
		return "", "", false
	}
	if len(sess.Agents) == 0 {
		return "", "", false
	}
	pick := sess.Agents[0]
	if active := sess.Meta.ActiveAgent; active != "" {
		for _, a := range sess.Agents {
			if a.Name == active {
				pick = a
				break
			}
		}
	}
	return pick.Provider, pick.ModelID, true
}

// SetActiveAgent updates meta.json's active_agent field. The named
// agent must already exist in agents.json.
func SetActiveAgent(layout config.Layout, id, name string) error {
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	found := false
	for _, a := range sess.Agents {
		if a.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("agent %q not found in session %q", name, id)
	}
	sess.Meta.ActiveAgent = name
	sess.Meta.LastActive = time.Now().UTC()
	return SaveMeta(layout, id, sess.Meta)
}
