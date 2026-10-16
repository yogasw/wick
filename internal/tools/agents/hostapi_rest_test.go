package agents_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/services/plugin/hostapi"
	agentstool "github.com/yogasw/wick/internal/tools/agents"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

type users []*entity.User

func (u users) ListUsers(context.Context) ([]*entity.User, error) { return u, nil }
func (u users) GetUserByID(_ context.Context, id string) (*entity.User, error) {
	for _, x := range u {
		if x.ID == id {
			return x, nil
		}
	}
	return nil, nil
}
func (u users) GetUserFilterTagIDs(context.Context, string) []string { return []string{"t1"} }

type plugins map[string]hostapi.PluginInfo

func (p plugins) Plugin(k string) (hostapi.PluginInfo, bool) { v, ok := p[k]; return v, ok }

// TestHostAPIRestThroughTicketMount sends a Host API Rest call through
// wick's own /api mount (TicketRESTShim -> CLIAPIAuthMW -> TicketAPIAuthMW):
// the plugin's forged Authorization never reaches the auth middleware, the
// acting user and the machine mark do, and paths outside the ticket
// allowlist stay 404.
func TestHostAPIRestThroughTicketMount(t *testing.T) {
	var seen struct {
		user, machine, auth, path string
	}
	gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := login.GetUser(r.Context()); u != nil {
			seen.user = u.ID
		}
		seen.machine, _ = login.MachineCaller(r.Context())
		seen.auth, seen.path = r.Header.Get("Authorization"), r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	router := http.NewServeMux()
	router.Handle(agentstool.TicketRESTBase+"/", agentstool.TicketRESTShim(agentstool.CLIAPIAuthMW(agentstool.TicketAPIAuthMW(gated))))

	us := users{{ID: "u1", Name: "Ada", Approved: true}}
	srv := hostapi.NewServer(hostapi.Deps{
		Plugins: plugins{"sync": {Key: "sync", Kind: "job", Scopes: []string{"rest.read"},
			Resources: &wickplugin.HostResources{Rest: []wickplugin.HostRestResource{{Method: "GET", Path: "/api/tickets/**"}, {Method: "GET", Path: "/api/sessions"}}}}},
		Users: us, Principals: us, Router: router,
	})
	ctx := hostapi.WithCaller(context.Background(), hostapi.Caller{PluginKey: "sync", UserID: "u1", Source: hostapi.SourceCaller})

	out, err := srv.Rest(ctx, &hostpb.RestRequest{Path: "/api/tickets/p1", Headers: map[string]string{"Authorization": "Bearer wick_pat_forged"}})
	if err != nil || out.Status != 200 {
		t.Fatalf("rest = %v %v", out, err)
	}
	if seen.user != "u1" || seen.machine != "plugin:sync" || seen.auth != "" || seen.path != "/tools/agents/api/tickets/p1" {
		t.Fatalf("reached the handler as %+v", seen)
	}
	// Declared, but not a ticket endpoint: the real shim refuses it.
	out, err = srv.Rest(ctx, &hostpb.RestRequest{Path: "/api/sessions"})
	if err != nil || out.Status != http.StatusNotFound {
		t.Fatalf("non-ticket path = %v %v", out, err)
	}
	var body map[string]string
	_ = json.Unmarshal(out.Body, &body)
	if body["error"] != "unknown endpoint" {
		t.Fatalf("body = %s", out.Body)
	}
}
