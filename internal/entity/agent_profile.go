package entity

import "time"

// AgentProfile is a reusable sub-agent role: the identity half of the
// multi-agent feature. One row = one role ("researcher", "code-reviewer")
// that any leader can delegate to, across every conversation.
//
// A profile is NOT a grant. Access always attaches to the human who
// triggered the root delegation; AllowedTagIDs can only NARROW that set,
// never widen it (see delegation.EffectiveTags). Storing a broad tag list
// here does not hand the sub-agent anything the triggering user lacks.
//
// Generalisation of Presets: a preset is only a system prompt, while a
// profile also pins provider + model + tool ACL + turn budget.
type AgentProfile struct {
	ID string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	// ProjectID scopes this role. Empty = global: reachable from every
	// project. Non-empty = owned by that project and invisible elsewhere.
	// A project row whose Key matches a global row SHADOWS it for sessions
	// in that project; the global row is untouched for everyone else.
	// See delegation.ResolveScoped.
	ProjectID string `gorm:"type:varchar(64);not null;default:'';index:idx_profile_scope,unique,priority:1" json:"project_id"`
	// Key is the stable handle the LLM passes to wick_delegate. Unique
	// within a scope, lowercase-kebab by convention ("code-reviewer").
	Key         string `gorm:"type:varchar(128);not null;index:idx_profile_scope,unique,priority:2" json:"key"`
	Name        string `gorm:"type:varchar(256);not null;default:''" json:"name"`
	Description string `gorm:"type:text;not null;default:''" json:"description"`
	Icon        string `gorm:"type:varchar(32);not null;default:''" json:"icon"`

	// Provider is the agent runtime ("claude" | "codex" | "gemini").
	// Resolved through the existing pool factory — a sub-agent is a
	// normal provider spawn, not a new runtime.
	Provider string `gorm:"type:varchar(64);not null" json:"provider"`
	// Model is a provider-specific model id. Empty = provider default.
	Model string `gorm:"type:varchar(128);not null;default:''" json:"model"`
	// SystemPrompt is injected into the child spawn via
	// provider.SpawnOptions.Preset (--append-system-prompt or the
	// per-provider equivalent).
	SystemPrompt string `gorm:"type:text;not null;default:''" json:"system_prompt"`

	// AllowedTagIDs is an OPTIONAL narrowing filter, stored as a JSON
	// array of tag ids. Empty = inherit the triggering user's tags in
	// full. Non-empty = intersect with them. See delegation.EffectiveTags.
	AllowedTagIDs string `gorm:"type:text;not null;default:'[]'" json:"allowed_tag_ids"`
	// AllowedNativeTools is a JSON array of provider-native tool names.
	//
	// NOT WIRED. The value is stored and returned by the API, but nothing
	// forwards it to the spawn as --allowedTools, so setting it has no
	// effect on what a sub-agent can call. Do not present it as a control
	// until a spawn path reads it.
	AllowedNativeTools string `gorm:"type:text;not null;default:'[]'" json:"allowed_native_tools"`
	// StrictMCP is intended to drop the host's own MCP servers
	// (~/.claude.json) from the child spawn.
	//
	// NOT WIRED, and it is NOT a security boundary. No spawn passes
	// --strict-mcp-config any more: wick INJECTS its own server per spawn via
	// --mcp-config (carrying that caller's token), so isolation would only
	// affect the user's OTHER servers, and the env switch that used to control
	// it globally was removed once each spawn gained its own credential.
	//
	// This field is read by nobody, so a sub-agent inherits whatever MCP
	// servers the host CLI has configured regardless of what a profile says.
	// Restricting what a role may REACH is enforced server-side by tags and
	// per-op access, not here.
	StrictMCP bool `gorm:"not null;default:true" json:"strict_mcp"`

	DefaultMaxTurns int `gorm:"not null;default:12" json:"default_max_turns"`
	// DefaultMode is "async" unless a role opts into "sync". Empty is
	// read as async too (see delegation.NormalizeMode): only an explicit
	// "sync" makes a caller block, so a role created without an opinion
	// runs in the background.
	DefaultMode string `gorm:"type:varchar(16);not null;default:'async'" json:"default_mode"`
	// DefaultWorkspace is "shared" today. "worktree" lands in Phase 3.
	DefaultWorkspace string `gorm:"type:varchar(16);not null;default:'shared'" json:"default_workspace"`
	// DefaultMemoryMode decides what this role is told about the rest of
	// the conversation beyond its own task: no_history, state_summary
	// (the default), relevant_chunks, or full_history. Empty means the
	// system default rather than "nothing".
	DefaultMemoryMode string `gorm:"type:varchar(32);not null;default:''" json:"default_memory_mode"`
	// CanDelegate marks a profile as eligible to be a leader, i.e. to
	// call wick_delegate itself (nested delegation). Forced false for
	// providers without MCP tool-use.
	CanDelegate bool `gorm:"not null;default:false" json:"can_delegate"`
	// AllowTakeOver lets a human send messages straight into a running
	// sub-agent of this role. Off by default: steering a sub-agent means
	// the result is no longer purely that role's own work, so it is opted
	// into per role rather than granted everywhere. Delegations that were
	// steered are flagged UserSteered and the leader is told.
	AllowTakeOver bool `gorm:"not null;default:false" json:"allow_take_over"`
	// DefaultMaxTokens caps token spend for one delegation of this role.
	// 0 = uncapped by the profile; the per-tree budget still applies.
	DefaultMaxTokens int `gorm:"not null;default:0" json:"default_max_tokens"`

	// Locked freezes this role's behaviour. While true, no edit and no
	// delete is accepted from ANY surface — web UI or MCP. Unlocking is a
	// UI-only action, so an agent can never widen its own definition.
	// Distinct from Disabled: a disabled role is switched off, a locked
	// role is switched in stone.
	Locked bool `gorm:"not null;default:false" json:"locked"`

	Disabled  bool      `gorm:"not null;default:false" json:"disabled"`
	CreatedBy string    `gorm:"type:varchar(128);not null;default:''" json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AgentProfile) TableName() string { return "agent_profiles" }
