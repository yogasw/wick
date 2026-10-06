// Package slack — manifest.go: the Slack app manifest wick generates for a
// Team agent in Custom mode (one Slack app per agent).
//
// Scopes and events come from requirements.go, never typed here. Slack
// sets the app photo outside the manifest, so there is no field for it.

package slack

import (
	"encoding/json"
	"net/url"
	"strings"
)

// DefaultManifestColor is the background colour a manifest gets when the
// agent has none.
const DefaultManifestColor = "#004492"

// Slack's own limits on manifest fields. A manifest over them is refused
// on create, so the generator trims instead.
const (
	manifestNameMax        = 35
	manifestDescriptionMax = 140
	manifestLongDescMin    = 175
	manifestLongDescMax    = 4000
	MaxSuggestedPrompts    = 4
)

// SuggestedPrompt is one prompt chip of Slack's agent view.
type SuggestedPrompt struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// ManifestInput is what the agent contributes to its manifest.
type ManifestInput struct {
	Name             string
	Description      string
	LongDescription  string
	Color            string
	AgentDescription string
	SuggestedPrompts []SuggestedPrompt
	// PublicURL is wick's public base URL; "" leaves redirect_urls out.
	PublicURL string
	// UserScopes are the personal-connector user scopes ("" entries are
	// dropped). Empty = the app gets no user token.
	UserScopes []string
}

// Manifest mirrors the subset of Slack's app manifest wick fills. Field
// order follows Slack's own export so a diff against it stays readable.
type Manifest struct {
	DisplayInformation ManifestDisplay  `json:"display_information"`
	Features           ManifestFeatures `json:"features"`
	OAuthConfig        ManifestOAuth    `json:"oauth_config"`
	Settings           ManifestSettings `json:"settings"`
}

type ManifestDisplay struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	BackgroundColor string `json:"background_color"`
	LongDescription string `json:"long_description,omitempty"`
}

type ManifestFeatures struct {
	AppHome   ManifestAppHome   `json:"app_home"`
	BotUser   ManifestBotUser   `json:"bot_user"`
	AgentView ManifestAgentView `json:"agent_view"`
}

type ManifestAppHome struct {
	HomeTabEnabled             bool `json:"home_tab_enabled"`
	MessagesTabEnabled         bool `json:"messages_tab_enabled"`
	MessagesTabReadOnlyEnabled bool `json:"messages_tab_read_only_enabled"`
}

type ManifestBotUser struct {
	DisplayName  string `json:"display_name"`
	AlwaysOnline bool   `json:"always_online"`
}

type ManifestAgentView struct {
	AgentDescription string            `json:"agent_description,omitempty"`
	SuggestedPrompts []SuggestedPrompt `json:"suggested_prompts"`
}

type ManifestOAuth struct {
	RedirectURLs []string       `json:"redirect_urls,omitempty"`
	Scopes       ManifestScopes `json:"scopes"`
	PKCEEnabled  bool           `json:"pkce_enabled"`
}

type ManifestScopes struct {
	User []string `json:"user,omitempty"`
	Bot  []string `json:"bot"`
}

type ManifestSettings struct {
	EventSubscriptions           ManifestEvents        `json:"event_subscriptions"`
	Interactivity                ManifestInteractivity `json:"interactivity"`
	OrgDeployEnabled             bool                  `json:"org_deploy_enabled"`
	SocketModeEnabled            bool                  `json:"socket_mode_enabled"`
	TokenRotationEnabled         bool                  `json:"token_rotation_enabled"`
	AppLevelTokenRotationEnabled bool                  `json:"app_level_token_rotation_enabled"`
	IsMCPEnabled                 bool                  `json:"is_mcp_enabled"`
}

type ManifestEvents struct {
	BotEvents []string `json:"bot_events"`
}

type ManifestInteractivity struct {
	IsEnabled bool `json:"is_enabled"`
}

// GenerateManifest builds the manifest for one agent. Socket mode is on so
// the app needs no public request URL.
func GenerateManifest(in ManifestInput) Manifest {
	name := clipManifest(strings.TrimSpace(in.Name), manifestNameMax)
	if name == "" {
		name = "Wick Agent"
	}
	color := strings.TrimSpace(in.Color)
	if !isHexColor(color) {
		color = DefaultManifestColor
	}
	long := strings.TrimSpace(in.LongDescription)
	// Slack refuses a long_description under 175 characters, so a short
	// one is left out rather than failing the whole create.
	if len(long) < manifestLongDescMin {
		long = ""
	}
	prompts := make([]SuggestedPrompt, 0, MaxSuggestedPrompts)
	for _, p := range in.SuggestedPrompts {
		p.Title, p.Message = strings.TrimSpace(p.Title), strings.TrimSpace(p.Message)
		if p.Title == "" || p.Message == "" {
			continue
		}
		prompts = append(prompts, p)
		if len(prompts) == MaxSuggestedPrompts {
			break
		}
	}
	m := Manifest{
		DisplayInformation: ManifestDisplay{
			Name:            name,
			Description:     clipManifest(strings.TrimSpace(in.Description), manifestDescriptionMax),
			BackgroundColor: color,
			LongDescription: clipManifest(long, manifestLongDescMax),
		},
		Features: ManifestFeatures{
			AppHome:   ManifestAppHome{MessagesTabEnabled: true},
			BotUser:   ManifestBotUser{DisplayName: name},
			AgentView: ManifestAgentView{AgentDescription: strings.TrimSpace(in.AgentDescription), SuggestedPrompts: prompts},
		},
		OAuthConfig: ManifestOAuth{
			Scopes: ManifestScopes{User: nonEmpty(in.UserScopes), Bot: RequiredBotScopes()},
		},
		Settings: ManifestSettings{
			EventSubscriptions: ManifestEvents{BotEvents: RequiredBotEvents()},
			Interactivity:      ManifestInteractivity{IsEnabled: true},
			SocketModeEnabled:  true,
		},
	}
	if base := strings.TrimRight(strings.TrimSpace(in.PublicURL), "/"); base != "" {
		m.OAuthConfig.RedirectURLs = []string{base + "/manager/connectors/slack/oauth/callback"}
	}
	return m
}

// ManifestCreateURL is the api.slack.com link that opens "Create app" with
// the manifest prefilled.
func ManifestCreateURL(m Manifest) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return "https://api.slack.com/apps?new_app=1&manifest_json=" + url.QueryEscape(string(b)), nil
}

func clipManifest(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
