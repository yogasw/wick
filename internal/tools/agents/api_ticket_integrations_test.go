package agents

import (
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// A redacted secret coming back from the client must restore the stored one.
// Without this, saving an unrelated setting would silently unsign every
// endpoint the project has.
func TestNormaliseWebhooksKeepsStoredSecret(t *testing.T) {
	prev := project.TicketIntegrations{Webhooks: []project.TicketWebhook{
		{ID: "wh_1", URL: "https://abc.com/hook", Secret: "s3cret", Enabled: true},
	}}
	next := project.TicketIntegrations{Webhooks: []project.TicketWebhook{
		// Same row, URL edited, secret arrives as the sentinel.
		{ID: "wh_1", URL: "https://abc.com/hook-v2", Secret: secretRedacted, Enabled: true},
	}}

	if err := normaliseWebhooks(&next, prev); err != nil {
		t.Fatal(err)
	}
	if next.Webhooks[0].Secret != "s3cret" {
		t.Fatalf("secret = %q, want the stored value restored", next.Webhooks[0].Secret)
	}
	if next.Webhooks[0].URL != "https://abc.com/hook-v2" {
		t.Errorf("url edit lost: %q", next.Webhooks[0].URL)
	}
}

// An explicit empty secret is the operator deliberately unsigning an
// endpoint, which must be honoured rather than treated as "unchanged".
func TestNormaliseWebhooksClearsSecretWhenEmptied(t *testing.T) {
	prev := project.TicketIntegrations{Webhooks: []project.TicketWebhook{
		{ID: "wh_1", URL: "https://abc.com/hook", Secret: "s3cret", Enabled: true},
	}}
	next := project.TicketIntegrations{Webhooks: []project.TicketWebhook{
		{ID: "wh_1", URL: "https://abc.com/hook", Secret: "", Enabled: true},
	}}

	if err := normaliseWebhooks(&next, prev); err != nil {
		t.Fatal(err)
	}
	if next.Webhooks[0].Secret != "" {
		t.Fatalf("secret = %q, want cleared", next.Webhooks[0].Secret)
	}
}

func TestNormaliseWebhooksMintsIDAndValidates(t *testing.T) {
	next := project.TicketIntegrations{Webhooks: []project.TicketWebhook{
		{URL: "https://abc.com/hook", Enabled: true},
	}}
	if err := normaliseWebhooks(&next, project.TicketIntegrations{}); err != nil {
		t.Fatal(err)
	}
	if next.Webhooks[0].ID == "" {
		t.Error("new webhook got no id — its delivery log could not survive an edit")
	}

	// A blank row is an empty line in the editor, not an error.
	blank := project.TicketIntegrations{Webhooks: []project.TicketWebhook{{}}}
	if err := normaliseWebhooks(&blank, project.TicketIntegrations{}); err != nil {
		t.Fatalf("blank row should be dropped, got %v", err)
	}
	if len(blank.Webhooks) != 0 {
		t.Errorf("blank row kept: %+v", blank.Webhooks)
	}

	for _, bad := range []project.TicketWebhook{
		{Name: "no url", URL: ""},
		{Name: "scheme", URL: "ftp://abc.com/x"},
		{Name: "garbage", URL: "not a url"},
		{Name: "bad event", URL: "https://abc.com/h", Events: []string{"ticket.exploded"}},
	} {
		in := project.TicketIntegrations{Webhooks: []project.TicketWebhook{bad}}
		if err := normaliseWebhooks(&in, project.TicketIntegrations{}); err == nil {
			t.Errorf("accepted an invalid webhook: %+v", bad)
		}
	}
}

// Secrets must never reach a client.
func TestRedactIntegrationsHidesSecrets(t *testing.T) {
	in := project.TicketIntegrations{Webhooks: []project.TicketWebhook{
		{ID: "wh_1", URL: "https://abc.com/a", Secret: "s3cret"},
		{ID: "wh_2", URL: "https://abc.com/b"},
	}}

	out := RedactIntegrations(in)
	if out.Webhooks[0].Secret != secretRedacted {
		t.Errorf("signed webhook secret = %q, want the sentinel", out.Webhooks[0].Secret)
	}
	if out.Webhooks[1].Secret != "" {
		t.Errorf("unsigned webhook gained a secret: %q", out.Webhooks[1].Secret)
	}
	// The source must not be mutated — it is the live config.
	if in.Webhooks[0].Secret != "s3cret" {
		t.Error("RedactIntegrations mutated the stored config")
	}
}

// The token surface is an allowlist on purpose: a prefix match on /api would
// quietly make sessions, providers, and admin endpoints token-authable.
func TestIsTicketAPIPathAllowlist(t *testing.T) {
	allowed := []string{
		"/tools/agents/api/tickets",
		"/tools/agents/api/tickets/T-4F2A",
		"/tools/agents/api/tickets/T-4F2A/sessions/sess_1",
		"/tools/agents/api/notes",
		"/tools/agents/api/notes/n1",
		"/tools/agents/api/ticket-events",
		"/tools/agents/api/projects/p1/tickets",
		"/tools/agents/api/projects/p1/ticket-config",
	}
	for _, p := range allowed {
		if !isTicketAPIPath(p) {
			t.Errorf("isTicketAPIPath(%q) = false, want true", p)
		}
	}

	denied := []string{
		"/tools/agents/api/sessions",
		"/tools/agents/api/providers",
		"/tools/agents/api/projects/p1",
		"/tools/agents/api/projects",
		"/tools/agents/api/agent-profiles",
		"/tools/agents/api/data-tables/x/rows/1",
		"/tools/other/api/tickets",
		"/admin/users",
		"/tools/agents/api/me/ticket-prefs",
	}
	for _, p := range denied {
		if isTicketAPIPath(p) {
			t.Errorf("isTicketAPIPath(%q) = true, want false", p)
		}
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc123": "abc123",
		"bearer abc123": "abc123", // curl users type either
		"Bearer  pad  ": "pad",
		"":              "",
		"Basic abc":     "",
		"abc123":        "",
	}
	for header, want := range cases {
		r := httptest.NewRequest("GET", "/tools/agents/api/tickets", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got := bearerToken(r); got != want {
			t.Errorf("bearerToken(%q) = %q, want %q", header, got, want)
		}
	}
}

// The catalogue the UI renders must be exactly what the server can emit.
func TestEventCatalogueIsNonEmptyAndValid(t *testing.T) {
	all := ticket.AllEvents()
	if len(all) == 0 {
		t.Fatal("event catalogue is empty")
	}
	seen := map[string]bool{}
	for _, e := range all {
		if seen[e] {
			t.Errorf("duplicate event %q", e)
		}
		seen[e] = true
		if !ticket.ValidEvent(e) {
			t.Errorf("catalogued event %q fails ValidEvent", e)
		}
	}
}
