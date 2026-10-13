package session

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
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
	// use that provider instance's own default-model resolution. Every
	// provider type honors it: wick maps it to a WickModel.ID, the CLI
	// providers pass it to their binary via --model. A switch rewrites it
	// (empty when none is asked for), so a later switch back doesn't
	// resurrect a stale/foreign pin.
	ModelID string `json:"model_id,omitempty"`
	// Failover is the agent's failover bookkeeping: the HOME provider the
	// session belongs on and what an automatic failover already tried.
	// nil on entries written before failover existed, and on agents that
	// never switched.
	Failover *FailoverState `json:"failover,omitempty"`
}

// FailoverState is the HOME of one session agent plus the progress of the
// failover run in flight. Home is what the user (or the session's
// creation) picked; an automatic failover moves Provider but never Home,
// so the chain followed is always Home's and the session can go back to
// it once it recovers. A manual switch makes the new provider the home
// and forgets the run.
//
// Plain strings: provider imports this package, so the provider types
// cannot appear here.
type FailoverState struct {
	// HomeProvider is the home instance's "type/name" key.
	HomeProvider string `json:"home_provider,omitempty"`
	// HomeModelID is the model pin the home runs with ("" = its default).
	HomeModelID string `json:"home_model_id,omitempty"`
	// Tried lists the instance keys this run already failed on, so one
	// message never visits an instance twice.
	Tried []string `json:"tried,omitempty"`
	// MsgKey identifies the user turn the run is for (the message plus a
	// mark unique to that send, so a repeated "ok" is a new run). A new
	// key starts a new run: Tried and Hops only count within one MsgKey.
	MsgKey string `json:"msg_key,omitempty"`
	// Hops is how many automatic switches this run made.
	Hops int `json:"hops,omitempty"`
	// Since is when this run's first failure happened (zero when no run
	// is in progress).
	Since time.Time `json:"since,omitempty"`
	// StandIns lists every instance a failover moved the agent onto
	// since it last sat on its home, in order. Unlike Tried it outlives
	// the run: a return home names them all, since none of their turns
	// is in the home's history. Cleared once back on the home.
	StandIns []string `json:"stand_ins,omitempty"`
}

// AwayFromHome reports whether st records a failover run in progress.
func (st *FailoverState) AwayFromHome() bool {
	return st != nil && (len(st.Tried) > 0 || st.Hops > 0)
}

// RunFor is the state as seen by the run for msgKey: st itself (a copy)
// when that run is the one recorded, otherwise the same home with a fresh
// run — no Tried, no Hops, Since = now. An empty msgKey never matches, so
// every call starts a new run. Both the reader (before PickTarget) and
// the writer (the switch that records a hop) go through here, so they
// cannot disagree on what "this run" holds. nil st = no home yet.
func (st *FailoverState) RunFor(msgKey string, now time.Time) FailoverState {
	var out FailoverState
	if st != nil {
		out = *st
		out.Tried = append([]string(nil), st.Tried...)
		out.StandIns = append([]string(nil), st.StandIns...)
	}
	if msgKey == "" || out.MsgKey != msgKey {
		out.EndRun()
		out.MsgKey, out.Since = msgKey, now
	}
	return out
}

// EndRun forgets the run (Tried, Hops, MsgKey, Since) and keeps the home
// and StandIns.
func (st *FailoverState) EndRun() {
	st.Tried, st.Hops, st.MsgKey, st.Since = nil, 0, "", time.Time{}
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

// agentsLocks serialize every read-modify-write of one session's
// agents.json inside this process (striped by path, so the set never
// grows). Without it two writers — a provider switch and the store
// parking a resume id at the end of the same turn — each load, edit and
// save, and the later save silently undoes the earlier one.
var agentsLocks [64]sync.Mutex

func agentsLock(layout config.Layout, id string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(layout.SessionAgents(id)))
	return &agentsLocks[h.Sum32()%uint32(len(agentsLocks))]
}

// UpdateAgents is the locked read-modify-write of agents.json: fn gets
// the current entries and returns the new list and whether to save it.
// fn's error is returned as is, with nothing written. fn must not call
// another agents.json writer of the same session (the lock is not
// reentrant).
func UpdateAgents(layout config.Layout, id string, fn func([]AgentEntry) ([]AgentEntry, bool, error)) error {
	mu := agentsLock(layout, id)
	mu.Lock()
	defer mu.Unlock()
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	agents, save, err := fn(sess.Agents)
	if err != nil || !save {
		return err
	}
	return SaveAgents(layout, id, agents)
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
	return UpdateAgents(layout, id, func(agents []AgentEntry) ([]AgentEntry, bool, error) {
		for _, a := range agents {
			if a.Name == name {
				return nil, false, fmt.Errorf("agent %q already exists in session %q", name, id)
			}
		}
		return append(agents, AgentEntry{
			Name:      name,
			Provider:  provider,
			Status:    "idle",
			CreatedAt: time.Now().UTC(),
		}), true, nil
	})
}

// SetAgentProvider repoints an existing agent entry at another provider
// instance, leaving the rest of the entry (resume id, caps) intact.
//
// Distinct from AddAgent, which refuses a name that already exists. Used
// to repair an entry created with no provider, and by a workflow node
// repointing a reused session: the spawn path has to fill one in, and
// re-adding would drop the resume id the entry already carries. Like a
// manual switch, the new provider becomes the home and any failover run
// is over. No-op when the entry doesn't exist.
func SetAgentProvider(layout config.Layout, id, name, providerKey string) error {
	return updateAgent(layout, id, name, nil, func(a *AgentEntry) bool {
		a.Provider = providerKey
		a.Failover = &FailoverState{HomeProvider: providerKey, HomeModelID: a.ModelID}
		return true
	})
}

// SetModelIDIfEmpty pins a model only when the entry carries no pin.
//
// Inherited defaults use this rather than SetModelID: a session already
// pinned to a model chose that deliberately, and a project default must
// not silently move it. No-op when the entry doesn't exist or already has
// a pin.
func SetModelIDIfEmpty(layout config.Layout, id, name, modelID string) error {
	return updateAgent(layout, id, name, nil, func(a *AgentEntry) bool {
		if a.ModelID != "" {
			return false
		}
		a.ModelID = modelID
		a.syncHomeModel()
		return true
	})
}

// SetCLISessionID writes (or clears, with "") the CLI resume id on the
// agent entry. No-op when the entry doesn't exist.
func SetCLISessionID(layout config.Layout, id, name, cliID string) error {
	return updateAgent(layout, id, name, nil, func(a *AgentEntry) bool {
		a.CLISessionID = cliID
		return true
	})
}

// SetMaxTurns persists the per-spawn turn cap on the agent entry,
// creating it if missing. 0 = unlimited (provider default).
func SetMaxTurns(layout config.Layout, id, name string, maxTurns int) error {
	if name == "" {
		return ErrNoAgentName
	}
	return updateAgent(layout, id, name, newIdleAgent, func(a *AgentEntry) bool {
		a.MaxTurns = maxTurns
		return true
	})
}

// SetThinkingTokens persists the resolved MAX_THINKING_TOKENS env value on
// the agent entry, creating it if missing. Empty = unset (provider default,
// thinking on); "0" = disabled; "<n>" = budget. Always persisted (including
// "") so switching a reused session back to full thinking clears a prior value.
func SetThinkingTokens(layout config.Layout, id, name, v string) error {
	if name == "" {
		return ErrNoAgentName
	}
	return updateAgent(layout, id, name, newIdleAgent, func(a *AgentEntry) bool {
		a.ThinkingTokens = v
		return true
	})
}

// SetModelID persists the pinned model id on the agent entry, creating it
// if missing. Empty = unset (the active provider's own default applies).
func SetModelID(layout config.Layout, id, name, modelID string) error {
	if name == "" {
		return ErrNoAgentName
	}
	return updateAgent(layout, id, name, newIdleAgent, func(a *AgentEntry) bool {
		a.ModelID = modelID
		a.syncHomeModel()
		return true
	})
}

// syncHomeModel keeps the home's model in step with a pin set while the
// agent is on its home: callers set the provider first (which records the
// home with the pin it had then) and the model after. A pin set on a
// stand-in is the stand-in's and leaves the home alone.
func (a *AgentEntry) syncHomeModel() {
	if a.Failover != nil && a.Failover.HomeProvider != "" && a.Failover.HomeProvider == a.Provider {
		a.Failover.HomeModelID = a.ModelID
	}
}

// newIdleAgent is the entry the creating setters add for an unknown name.
func newIdleAgent(name string) AgentEntry {
	return AgentEntry{Name: name, Status: "idle", CreatedAt: time.Now().UTC()}
}

// updateAgent runs edit on the entry named name under the session's
// agents lock and saves when edit reports a change. An unknown name is
// created from create, or left alone (no write) when create is nil.
func updateAgent(layout config.Layout, id, name string, create func(string) AgentEntry, edit func(*AgentEntry) bool) error {
	return UpdateAgents(layout, id, func(agents []AgentEntry) ([]AgentEntry, bool, error) {
		for i := range agents {
			if agents[i].Name == name {
				return agents, edit(&agents[i]), nil
			}
		}
		if create == nil {
			return agents, false, nil
		}
		a := create(name)
		edit(&a)
		return append(agents, a), true, nil
	})
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

// SetFailoverState writes st onto the agent entry. No-op when the entry
// doesn't exist: the state belongs to an agent already running.
func SetFailoverState(layout config.Layout, id, name string, st FailoverState) error {
	if name == "" {
		return ErrNoAgentName
	}
	st.Tried = append([]string(nil), st.Tried...)
	return updateAgent(layout, id, name, nil, func(a *AgentEntry) bool {
		a.Failover = &st
		return true
	})
}

// EndFailoverRun closes the agent's failover run (EndRun) and keeps its
// home: the turn the run was for finished. No-op when the entry doesn't
// exist or has no run.
func EndFailoverRun(layout config.Layout, id, name string) error {
	return updateAgent(layout, id, name, nil, func(a *AgentEntry) bool {
		if a.Failover == nil || (a.Failover.MsgKey == "" && len(a.Failover.Tried) == 0 && a.Failover.Hops == 0 && a.Failover.Since.IsZero()) {
			return false
		}
		a.Failover.EndRun()
		return true
	})
}

// ClearFailoverState drops the agent's failover state, home included.
// No-op when the entry doesn't exist or carries none.
func ClearFailoverState(layout config.Layout, id, name string) error {
	return updateAgent(layout, id, name, nil, func(a *AgentEntry) bool {
		if a.Failover == nil {
			return false
		}
		a.Failover = nil
		return true
	})
}
