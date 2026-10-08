package team

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/entity"
)

// NativeTools are the provider-native tools the Tools & features tab
// switches, in the order it lists them.
var NativeTools = []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch", "Bash", "Edit", "Write"}

// DefaultNativeTools is what a new agent starts with: the read-only tools
// on, the ones that change the machine (Bash, Edit, Write) off.
var DefaultNativeTools = []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch"}

// claudeToolNames maps a switch to every claude tool it covers: turning
// Edit off must also stop MultiEdit, and Bash its background helpers.
var claudeToolNames = map[string][]string{
	"Bash":      {"Bash", "BashOutput", "KillShell"},
	"Edit":      {"Edit", "MultiEdit", "NotebookEdit"},
	"Write":     {"Write"},
	"Read":      {"Read"},
	"Grep":      {"Grep"},
	"Glob":      {"Glob"},
	"WebFetch":  {"WebFetch"},
	"WebSearch": {"WebSearch"},
}

// ScopeProject is the BashRule scope that stands for the agent's own
// project folder, resolved at spawn.
const ScopeProject = "{project}"

// BashRule is one entry of an agent's Bash allow-list: Pattern is a
// gate.CommandRule glob, Scope ScopeProject or an absolute path the
// command's path arguments must stay under. An empty Scope falls back to
// the project folder too (the gate's default scope), never "anywhere".
type BashRule struct {
	Pattern string `json:"pattern"`
	Scope   string `json:"scope"`
}

// DecodeNativeTools reads AllowedNativeTools. "" (a row from before the
// column) is every tool, so an upgrade leaves old agents as they ran.
func DecodeNativeTools(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return slices.Clone(NativeTools)
	}
	var in []string
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		// Unreadable fails towards less: the read-only defaults.
		return slices.Clone(DefaultNativeTools)
	}
	return NormalizeNativeTools(in)
}

// NormalizeNativeTools keeps the known names, once each, in NativeTools
// order.
func NormalizeNativeTools(in []string) []string {
	out := []string{}
	for _, t := range NativeTools {
		if slices.Contains(in, t) {
			out = append(out, t)
		}
	}
	return out
}

// EncodeNativeTools is the column value of tools.
func EncodeNativeTools(tools []string) string {
	b, _ := json.Marshal(NormalizeNativeTools(tools))
	return string(b)
}

// ValidateNativeTools rejects a name the tab does not offer.
func ValidateNativeTools(tools []string) error {
	for _, t := range tools {
		if !slices.Contains(NativeTools, t) {
			return fmt.Errorf("unknown native tool %q", t)
		}
	}
	return nil
}

// DisallowedClaudeTools is the --disallowedTools list for an agent
// allowed only `allowed`: every claude tool behind a switch that is off.
func DisallowedClaudeTools(allowed []string) []string {
	var out []string
	for _, t := range NativeTools {
		if !slices.Contains(allowed, t) {
			out = append(out, claudeToolNames[t]...)
		}
	}
	return out
}

// NativeToolsEnforced reports whether providerType can be held to the
// native-tool switches and Bash rules. Only claude takes a tool deny list
// at spawn and routes Bash through the gate hook; on the others the
// settings are stored but do not bind, and the UI says so.
func NativeToolsEnforced(providerType string) bool {
	return providerType == "" || providerType == "claude"
}

// DecodeBashRules reads BashRules; unreadable is none.
func DecodeBashRules(raw string) []BashRule {
	var out []BashRule
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []BashRule{}
	}
	return out
}

// EncodeBashRules is the column value of rules.
func EncodeBashRules(rules []BashRule) string {
	if rules == nil {
		rules = []BashRule{}
	}
	b, _ := json.Marshal(rules)
	return string(b)
}

// ValidateBashRule rejects a rule that could chain or substitute a
// second command (`|`, `;`, `&&`, backtick, `$(` and the other shell
// metacharacters the gate refuses) and a scope that is neither empty,
// ScopeProject nor an absolute path.
func ValidateBashRule(r BashRule) error {
	p := strings.TrimSpace(r.Pattern)
	if p == "" {
		return fmt.Errorf("pattern is empty")
	}
	for _, bad := range []string{"|", ";", "&&", "`", "$("} {
		if strings.Contains(p, bad) {
			return fmt.Errorf("pattern %q contains %q: one command per rule", p, bad)
		}
	}
	if err := (gate.CommandRule{Pattern: p}).Validate(); err != nil {
		return fmt.Errorf("pattern %q: %v", p, err)
	}
	switch s := strings.TrimSpace(r.Scope); {
	case s == "", s == ScopeProject:
	case filepath.IsAbs(s):
	default:
		return fmt.Errorf("scope %q must be empty, %s or an absolute path", s, ScopeProject)
	}
	return nil
}

// ResolveBashRules turns rules into gate rules, ScopeProject becoming
// projectDir. With no projectDir a ScopeProject rule is dropped rather
// than left unscoped, so it never allows more than it says.
func ResolveBashRules(rules []BashRule, projectDir string) []gate.CommandRule {
	out := []gate.CommandRule{}
	for _, r := range rules {
		if ValidateBashRule(r) != nil {
			continue
		}
		scope := strings.TrimSpace(r.Scope)
		if scope == ScopeProject {
			if projectDir == "" {
				continue
			}
			scope = projectDir
		}
		out = append(out, gate.CommandRule{Pattern: strings.TrimSpace(r.Pattern), Scope: scope})
	}
	return out
}

// DecodeSkillNames reads DisabledSkills; unreadable is none.
func DecodeSkillNames(raw string) []string {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

// EncodeSkillNames is the column value of names, sorted and deduplicated.
func EncodeSkillNames(names []string) string {
	out := []string{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	b, _ := json.Marshal(out)
	return string(b)
}

// Limits is what a spawn under a Team agent is held to besides its
// connector scope: the native tools, the Bash allow-list (scopes already
// resolved) and the skills left out of the catalog.
type Limits struct {
	AgentID        string
	NativeTools    []string
	BashRules      []gate.CommandRule
	DisabledSkills []string
	// ProjectDir is the agent's project folder ("" when it cannot be
	// resolved), the default scope of its Bash rules.
	ProjectDir string
}

// LimitsOf is p's Limits, resolving ScopeProject against projectDir.
func LimitsOf(p entity.AgentPersona, projectDir string) Limits {
	return Limits{
		AgentID:        p.ID,
		NativeTools:    DecodeNativeTools(p.AllowedNativeTools),
		BashRules:      ResolveBashRules(DecodeBashRules(p.BashRules), projectDir),
		DisabledSkills: DecodeSkillNames(p.DisabledSkills),
		ProjectDir:     projectDir,
	}
}

// LimitsFor returns the Limits of the Team agent sessionID works for.
// AgentFor follows the parent chain, so a sub-agent delegated under a
// Team session gets exactly its agent's limits — never wider. false for
// a session outside the Team.
func (s *Service) LimitsFor(ctx context.Context, sessionID string) (Limits, bool) {
	p := s.AgentFor(ctx, sessionID)
	if p == nil {
		return Limits{}, false
	}
	dir := ""
	if p.ProjectID != "" {
		if d, err := project.ResolvePath(s.layout, p.ProjectID); err == nil {
			dir = d
		}
	}
	return LimitsOf(*p, dir), true
}

// BashAllowed reports whether a Bash script may run on behalf of a session
// (or a project's agent): the same switch that decides whether the agent's
// sessions get the Bash tool. A session or project no Team agent owns is an
// ordinary one, which has Bash. A session whose agent chain is broken gets
// nothing, matching ScopeForSession.
func (s *Service) BashAllowed(ctx context.Context, sessionID, projectID string) bool {
	if s == nil {
		return true
	}
	agentID := ""
	if sessionID != "" {
		id, err := AgentOfSession(s.layout, sessionID)
		if err != nil {
			return false
		}
		agentID = id
	} else if projectID != "" {
		agentID = s.AgentOfProject(ctx, projectID)
	}
	if agentID == "" {
		return true
	}
	p, err := s.Get(ctx, agentID)
	if err != nil {
		return false
	}
	return slices.Contains(DecodeNativeTools(p.AllowedNativeTools), "Bash")
}
