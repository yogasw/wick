package slack

import (
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
)

func missing(have, want []string) []string {
	var out []string
	for _, w := range want {
		if !slices.Contains(have, w) {
			out = append(out, w)
		}
	}
	return out
}

// The generated manifest declares every scope and event the requirement
// table asks for, so a new app never starts out short.
func TestGeneratedManifestCoversRequirements(t *testing.T) {
	m := GenerateManifest(ManifestInput{Name: "Rekap"})
	for _, f := range Requirements() {
		if miss := missing(m.OAuthConfig.Scopes.Bot, f.BotScopes); len(miss) > 0 {
			t.Errorf("%s: manifest lacks scopes %v", f.Key, miss)
		}
		if miss := missing(m.Settings.EventSubscriptions.BotEvents, f.Events); len(miss) > 0 {
			t.Errorf("%s: manifest lacks events %v", f.Key, miss)
		}
	}
	if !m.Settings.SocketModeEnabled || !m.Settings.Interactivity.IsEnabled {
		t.Error("socket mode and interactivity must be on")
	}
}

// docs/slack-app-manifest.json is what operators copy for the shared
// channel; it must not lag the requirement table either.
func TestDocsManifestCoversRequirements(t *testing.T) {
	raw, err := os.ReadFile("../../../../docs/slack-app-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if miss := missing(m.OAuthConfig.Scopes.Bot, RequiredBotScopes()); len(miss) > 0 {
		t.Errorf("docs manifest lacks bot scopes %v", miss)
	}
	if miss := missing(m.Settings.EventSubscriptions.BotEvents, RequiredBotEvents()); len(miss) > 0 {
		t.Errorf("docs manifest lacks events %v", miss)
	}
}

func TestGenerateManifestFields(t *testing.T) {
	long := strings.Repeat("x", 10)
	m := GenerateManifest(ManifestInput{
		Name:             strings.Repeat("N", 50),
		Description:      "Recaps the day",
		LongDescription:  long,
		Color:            "red",
		AgentDescription: "I recap",
		PublicURL:        "https://wick.example/",
		UserScopes:       []string{"chat:write", " "},
		SuggestedPrompts: []SuggestedPrompt{
			{Title: "A", Message: "a"}, {Title: "", Message: "skip"},
			{Title: "B", Message: "b"}, {Title: "C", Message: "c"},
			{Title: "D", Message: "d"}, {Title: "E", Message: "e"},
		},
	})
	d := m.DisplayInformation
	if len([]rune(d.Name)) != manifestNameMax || m.Features.BotUser.DisplayName != d.Name {
		t.Errorf("name not clipped/mirrored: %q / %q", d.Name, m.Features.BotUser.DisplayName)
	}
	if d.BackgroundColor != DefaultManifestColor {
		t.Errorf("invalid colour should fall back, got %q", d.BackgroundColor)
	}
	if d.LongDescription != "" {
		t.Error("a long_description under Slack's minimum must be left out")
	}
	if got := len(m.Features.AgentView.SuggestedPrompts); got != MaxSuggestedPrompts {
		t.Errorf("prompts = %d, want %d", got, MaxSuggestedPrompts)
	}
	if want := []string{"https://wick.example/manager/connectors/slack/oauth/callback"}; !slices.Equal(m.OAuthConfig.RedirectURLs, want) {
		t.Errorf("redirect_urls = %v", m.OAuthConfig.RedirectURLs)
	}
	if !slices.Equal(m.OAuthConfig.Scopes.User, []string{"chat:write"}) {
		t.Errorf("user scopes = %v", m.OAuthConfig.Scopes.User)
	}
}

func TestManifestCreateURLRoundTrips(t *testing.T) {
	m := GenerateManifest(ManifestInput{Name: "Rekap & co"})
	link, err := ManifestCreateURL(m)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil || u.Host != "api.slack.com" || u.Query().Get("new_app") != "1" {
		t.Fatalf("bad link %q", link)
	}
	var back Manifest
	if err := json.Unmarshal([]byte(u.Query().Get("manifest_json")), &back); err != nil {
		t.Fatal(err)
	}
	if back.DisplayInformation.Name != "Rekap & co" {
		t.Errorf("name = %q", back.DisplayInformation.Name)
	}
}
