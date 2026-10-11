package hostapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

func TestGetConfigReadSecret(t *testing.T) {
	f := newFixture(t)
	ctx := as(Caller{PluginKey: "setup"})
	req := &hostpb.GetConfigRequest{Owner: "job:other", Key: "api_secret"}
	// Declared owner/key, but the scope is off by default: value withheld.
	got, err := f.srv.GetConfig(ctx, req)
	if err != nil || got.Value != "" || !got.Secret || !got.Found {
		t.Fatalf("read_secret off = %+v, %v", got, err)
	}
	_ = f.perms.Set(context.Background(), "setup", "config.read_secret", "", true, "admin")
	if got, _ = f.srv.GetConfig(ctx, req); got.Value != "s3cret" {
		t.Fatalf("read_secret allowed + declared = %+v", got)
	}
	// Allowed scope, undeclared key: still withheld.
	if got, _ = f.srv.GetConfig(ctx, &hostpb.GetConfigRequest{Owner: "job:other", Key: "other_secret"}); got.Value != "" {
		t.Fatalf("undeclared secret = %+v", got)
	}
	// One item blocked.
	_ = f.perms.Set(context.Background(), "setup", "config.read_secret", wickplugin.ConfigItem("job:other", "api_secret"), false, "admin")
	if got, _ = f.srv.GetConfig(ctx, req); got.Value != "" {
		t.Fatalf("blocked item = %+v", got)
	}
	// A plugin that never declared read_secret gets nothing either.
	if got, _ = f.srv.GetConfig(as(Caller{PluginKey: "cronpt"}), req); got.Value != "" {
		t.Fatalf("undeclared scope = %+v", got)
	}
	// Non-secret values of other owners need only config.read_other.
	if got, _ = f.srv.GetConfig(as(Caller{PluginKey: "cronpt"}), &hostpb.GetConfigRequest{Owner: "job:other", Key: "region"}); got.Value != "id" {
		t.Fatalf("plain other value = %+v", got)
	}
}

func TestIdentityRefusesBadPATAndUnapproved(t *testing.T) {
	f := newFixture(t)
	_, err := f.srv.GetTicket(as(Caller{PluginKey: "badpat"}), &hostpb.GetTicketRequest{Id: "T-1"})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "not a valid") {
		t.Fatalf("revoked PAT err = %v", err)
	}
	_, err = f.srv.GetTicket(as(Caller{PluginKey: "sync", UserID: "u3"}), &hostpb.GetTicketRequest{Id: "T-1"})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("unapproved caller err = %v", err)
	}
	_, err = f.srv.GetTicket(as(Caller{PluginKey: "sync", UserID: "ghost"}), &hostpb.GetTicketRequest{Id: "T-1"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unknown caller err = %v", err)
	}
	if f.tickets.lastUser != "" {
		t.Fatalf("ticket service reached as %q", f.tickets.lastUser)
	}
}

// TestEveryUserLevelMethodNeedsIdentity guards against an RPC name typo in
// server.go silently skipping identity: every served user-level RPC refuses
// a call with no person behind it.
func TestEveryUserLevelMethodNeedsIdentity(t *testing.T) {
	f := newFixture(t)
	f.srv.d.Sessions = &LayoutData{}
	ctx := as(Caller{PluginKey: "sync"})
	calls := map[string]func() error{
		"ListSessions":   func() error { _, err := f.srv.ListSessions(ctx, &hostpb.ListSessionsRequest{}); return err },
		"GetSessionMeta": func() error { _, err := f.srv.GetSessionMeta(ctx, &hostpb.GetSessionMetaRequest{Id: "s"}); return err },
		"SendToSession": func() error {
			_, err := f.srv.SendToSession(ctx, &hostpb.SendToSessionRequest{SessionId: "s", Text: "x"})
			return err
		},
		"GetTicket":    func() error { _, err := f.srv.GetTicket(ctx, &hostpb.GetTicketRequest{Id: "T-1"}); return err },
		"ListTickets":  func() error { _, err := f.srv.ListTickets(ctx, &hostpb.ListTicketsRequest{}); return err },
		"CreateTicket": func() error { _, err := f.srv.CreateTicket(ctx, &hostpb.CreateTicketRequest{Title: "x"}); return err },
		"UpdateTicket": func() error { _, err := f.srv.UpdateTicket(ctx, &hostpb.UpdateTicketRequest{Id: "T-1"}); return err },
		"AttachSession": func() error {
			_, err := f.srv.AttachSession(ctx, &hostpb.AttachSessionRequest{TicketId: "T-1", SessionId: "s"})
			return err
		},
	}
	for _, m := range wickplugin.HostMethods {
		if !m.UserLevel {
			continue
		}
		fn, ok := calls[m.Name]
		if !ok {
			continue // not served yet (W2b): Unimplemented before any identity
		}
		if err := fn(); status.Code(err) != codes.FailedPrecondition {
			t.Errorf("%s without a user: err = %v, want FailedPrecondition", m.Name, err)
		}
	}
}

type failingStore struct{ *MemoryPermissionStore }

func (failingStore) List(context.Context, string) ([]entity.PluginHostPermission, error) {
	return nil, errors.New("db down")
}

func TestPermissionStoreErrorDenies(t *testing.T) {
	f := newFixture(t)
	f.srv.d.Perms = NewPermissions(failingStore{NewMemoryPermissionStore()})
	_, err := f.srv.ListUsers(as(Caller{PluginKey: "sync"}), &hostpb.ListUsersRequest{})
	var pd *host.PermissionDeniedError
	if !errors.As(err, &pd) || !strings.Contains(pd.Message, "unavailable") {
		t.Fatalf("store down err = %v, want PermissionDenied (fail closed)", err)
	}
	if _, err := f.srv.Whoami(as(Caller{PluginKey: "sync"}), &hostpb.WhoamiRequest{}); err != nil {
		t.Fatalf("whoami must still answer: %v", err)
	}
}

func TestUnknownScopeIsReportedAndDenied(t *testing.T) {
	p := NewPermissions(nil)
	pl := PluginInfo{Key: "future", Scopes: []string{"users.read", "notes.read"}}
	if d := p.Decide(context.Background(), pl, "notes.read", ""); d.Allowed || d.Status != StatusUnknown {
		t.Fatalf("unknown scope = %+v", d)
	}
	var found bool
	for _, st := range p.Statuses(context.Background(), pl) {
		if st.Scope == "notes.read" {
			found = st.Decision.Status == StatusUnknown && st.Declared
		}
	}
	if !found {
		t.Fatal("admin statuses do not list the unknown scope")
	}
}

func TestAuditRecordAfterClose(t *testing.T) {
	m := &memoryAudit{}
	a := NewAudit(m)
	a.Start(context.Background())
	a.Record(entity.PluginHostCall{PluginKey: "p", RPC: "Whoami"})
	a.Close()
	a.Close()                                                    // idempotent
	a.Record(entity.PluginHostCall{PluginKey: "p", RPC: "late"}) // must not panic
	if a.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", a.Dropped())
	}
	if got := m.snapshot(); len(got) != 1 || got[0].RPC != "Whoami" {
		t.Fatalf("flushed = %+v", got)
	}
	// Never started: Close returns at once and Record still never panics.
	b := NewAudit(m)
	b.Close()
	b.Record(entity.PluginHostCall{})
}
