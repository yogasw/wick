package a2aserver

import (
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Agent is what the A2A server needs to know about a Team agent.
type Agent struct {
	ID          string
	OwnerUserID string
	ProjectID   string
	// Name and Description are the persona's: the project's name and
	// description, the tagline in front.
	Name        string
	Description string
	Disabled    bool
	Prompts     []Prompt
}

// Prompt is one suggested prompt of the agent.
type Prompt struct {
	Title   string
	Message string
}

// securityScheme names the Bearer scheme the card advertises.
const securityScheme a2a.SecuritySchemeName = "bearer"

// EndpointPath is the JSON-RPC endpoint path of agentID.
func EndpointPath(agentID string) string { return "/integrations/a2a/" + agentID }

// CardPath is the agent card path of agentID.
func CardPath(agentID string) string {
	return EndpointPath(agentID) + "/.well-known/agent-card.json"
}

// BuildCard is a's agent card, served from baseURL (wick's public URL).
// Skills are the suggested prompts; an agent without any advertises one
// generic chat skill, since a card with no skill tells a caller nothing.
func BuildCard(a Agent, baseURL string) *a2a.AgentCard {
	name := strings.TrimSpace(a.Name)
	if name == "" {
		name = a.ID
	}
	desc := strings.TrimSpace(a.Description)
	if desc == "" {
		desc = name + " — a wick Team agent."
	}
	skills := make([]a2a.AgentSkill, 0, len(a.Prompts))
	for i, p := range a.Prompts {
		title, msg := strings.TrimSpace(p.Title), strings.TrimSpace(p.Message)
		if title == "" && msg == "" {
			continue
		}
		if title == "" {
			title = msg
		}
		if msg == "" {
			msg = title
		}
		skills = append(skills, a2a.AgentSkill{
			ID: fmt.Sprintf("prompt-%d", i+1), Name: title, Description: msg,
			Examples: []string{msg}, Tags: []string{"suggested-prompt"},
		})
	}
	if len(skills) == 0 {
		skills = append(skills, a2a.AgentSkill{
			ID: "chat", Name: "Chat", Description: "Talk to " + name + " in plain text.", Tags: []string{"chat"},
		})
	}
	return &a2a.AgentCard{
		Name:        name,
		Description: desc,
		Version:     "1.0.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(strings.TrimRight(baseURL, "/")+EndpointPath(a.ID), a2a.TransportProtocolJSONRPC),
		},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             skills,
		SecuritySchemes: a2a.NamedSecuritySchemes{
			securityScheme: a2a.HTTPAuthSecurityScheme{Scheme: "Bearer", Description: "Connection API key or wick Personal Access Token"},
		},
		SecurityRequirements: a2a.SecurityRequirementsOptions{{securityScheme: a2a.SecuritySchemeScopes{}}},
	}
}
