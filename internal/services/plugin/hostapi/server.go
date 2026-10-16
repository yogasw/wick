package hostapi

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// Server implements hostpb.HostServer for both doors.
type Server struct {
	hostpb.UnimplementedHostServer
	d     Deps
	users *userCache
	now   func() time.Time

	restMu  sync.Mutex
	restSem map[string]chan struct{} // per-plugin Rest slots
}

var _ hostpb.HostServer = (*Server)(nil)

// NewServer returns a Server over d. A nil d.Perms decides on defaults only.
func NewServer(d Deps) *Server {
	if d.Perms == nil {
		d.Perms = NewPermissions(nil)
	}
	s := &Server{d: d, now: time.Now}
	if d.Users != nil {
		s.users = &userCache{dir: d.Users, tags: d.UserTags, ttl: UsersCacheTTL, now: func() time.Time { return s.now() }}
	}
	return s
}

// call is what one RPC is running as.
type call struct {
	caller Caller
	plugin PluginInfo
	// userID/source: the person a user-level call runs for.
	userID string
	source string
	note   string
}

// gate describes one call for run: the RPC, the scope and item it needs
// (scope "" = none), a note for the audit row and whether it must run as a
// person even when HostMethods does not say so (an Invoke method). A
// non-nil reject fails the call right after the plugin is authenticated,
// still audited (a malformed Rest path).
type gate struct {
	rpc, scope, item string
	note             string
	userLevel        bool
	reject           error
}

// run is the single gate every RPC goes through: authenticated plugin,
// permission (scope "" = no scope needed), identity for user-level RPCs,
// the call itself, then one audit row.
func (s *Server) run(ctx context.Context, rpc, scope, item string, fn func(c *call) error) error {
	return s.runGate(ctx, gate{rpc: rpc, scope: scope, item: item}, fn)
}

func (s *Server) runGate(ctx context.Context, g gate, fn func(c *call) error) error {
	start := s.now()
	caller, ok := CallerFrom(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no plugin identity on the call")
	}
	c := &call{caller: caller, plugin: s.pluginInfo(caller), userID: caller.UserID, source: caller.Source, note: g.note}
	err := func() error {
		if g.reject != nil {
			return g.reject
		}
		if g.scope != "" {
			dec := s.d.Perms.Decide(ctx, c.plugin, g.scope, g.item)
			if !dec.Allowed {
				return &host.PermissionDeniedError{Scope: g.scope, Item: g.item, Plugin: c.plugin.Key, Message: dec.Reason}
			}
			if dec.Status == StatusUndeclared {
				c.addNote("undeclared")
			}
		}
		if m, _ := wickplugin.LookupHostMethod(g.rpc); m.UserLevel || g.userLevel {
			if err := s.resolveUser(ctx, c); err != nil {
				return err
			}
		}
		return fn(c)
	}()
	s.audit(c, g.rpc, g.scope, g.item, start, err)
	return err
}

func (s *Server) pluginInfo(caller Caller) PluginInfo {
	info := PluginInfo{Key: caller.PluginKey, Kind: caller.Kind}
	if s.d.Plugins != nil {
		if p, found := s.d.Plugins.Plugin(caller.PluginKey); found {
			info = p
		}
	}
	if info.Kind == "" {
		info.Kind = caller.Kind
	}
	return info
}

// addNote appends to the audit note.
func (c *call) addNote(n string) {
	if c.note == "" {
		c.note = n
		return
	}
	c.note += "; " + n
}

// resolveUser picks who a user-level call runs as: the caller the door
// reported, else the owner of the plugin's wick_pat. There is no fallback
// identity — no person means FailedPrecondition, and so does a person who
// is unknown or not approved.
func (s *Server) resolveUser(ctx context.Context, c *call) error {
	if c.userID == "" {
		if err := s.patUser(ctx, c); err != nil {
			return err
		}
	} else if c.source == "" {
		c.source = SourceCaller
	}
	if s.users == nil {
		return status.Error(codes.FailedPrecondition, "user directory unavailable; cannot verify the acting user")
	}
	u, err := s.users.identity(ctx, c.userID)
	if err != nil {
		return status.Errorf(codes.Unavailable, "look up acting user: %v", err)
	}
	if u == nil {
		return status.Error(codes.FailedPrecondition, "acting user ("+c.source+") does not exist")
	}
	if !u.Approved {
		return status.Error(codes.FailedPrecondition, "acting user ("+c.source+") is not approved")
	}
	return nil
}

// patUser resolves the owner of the plugin's wick_pat.
func (s *Server) patUser(ctx context.Context, c *call) error {
	noUser := status.Error(codes.FailedPrecondition,
		"no user identity — set "+PATConfigKey+" in the plugin config or run on behalf of a user")
	if s.d.Configs == nil || s.d.PATs == nil || c.plugin.ConfigOwner == "" {
		return noUser
	}
	pat, found, _, err := s.d.Configs.Config(ctx, c.plugin.ConfigOwner, PATConfigKey)
	if err != nil {
		return status.Errorf(codes.Unavailable, "read %s: %v", PATConfigKey, err)
	}
	if !found || strings.TrimSpace(pat) == "" {
		return noUser
	}
	uid, err := s.d.PATs.Authenticate(ctx, strings.TrimSpace(pat))
	if err != nil || uid == "" {
		return status.Error(codes.FailedPrecondition, PATConfigKey+" is not a valid personal access token (revoked or mistyped)")
	}
	c.userID, c.source = uid, SourcePAT
	return nil
}

func (s *Server) audit(c *call, rpc, scope, item string, start time.Time, err error) {
	if s.d.Audit == nil {
		return
	}
	code := codes.OK
	if err != nil {
		code = codeOf(err)
	}
	s.d.Audit.Record(entity.PluginHostCall{
		Ts: start.UTC(), PluginKey: c.plugin.Key, Kind: c.plugin.Kind, RPC: rpc, Scope: scope, Item: item,
		UserID: c.userID, Source: c.source, OK: err == nil, Code: code.String(),
		DurationMs: s.now().Sub(start).Milliseconds(), Note: c.note,
	})
}

func codeOf(err error) codes.Code {
	var pd *host.PermissionDeniedError
	switch {
	case errors.As(err, &pd):
		return codes.PermissionDenied
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Code()
	}
	return status.Code(err)
}

func unavailable(what string) error {
	return status.Errorf(codes.Unimplemented, "wick host API: %s is not available on this wick", what)
}

// ---- meta ----

// Whoami reports the plugin, its declared scopes and the ones it may use.
func (s *Server) Whoami(ctx context.Context, _ *hostpb.WhoamiRequest) (*hostpb.WhoamiResponse, error) {
	var out *hostpb.WhoamiResponse
	err := s.run(ctx, "Whoami", wickplugin.ScopeHostWhoami, "", func(c *call) error {
		out = &hostpb.WhoamiResponse{
			PluginKey: c.plugin.Key, PluginVersion: c.plugin.Version, Kind: c.plugin.Kind,
			ScopesDeclared: append([]string{}, c.plugin.Scopes...),
			ScopesAllowed:  s.allowedScopes(ctx, c.plugin),
			WickVersion:    s.d.WickVersion, HostApiVersion: host.APIVersion,
		}
		return nil
	})
	return out, err
}

// allowedScopes lists the scopes the plugin may call right now: every RPC
// scope the overlay/defaults allow, plus the declared declarative ones.
func (s *Server) allowedScopes(ctx context.Context, p PluginInfo) []string {
	out := []string{}
	for _, sc := range wickplugin.HostScopes {
		if sc.Declarative {
			if slices.Contains(p.Scopes, sc.Name) {
				out = append(out, sc.Name)
			}
			continue
		}
		if s.d.Perms.Decide(ctx, p, sc.Name, "").Allowed {
			out = append(out, sc.Name)
		}
	}
	return out
}

// ListMethods lists the Host API RPCs, then the Invoke methods (built-in
// aliases and RegisterMethod registrations), each with its scope.
func (s *Server) ListMethods(ctx context.Context, _ *hostpb.ListMethodsRequest) (*hostpb.ListMethodsResponse, error) {
	out := &hostpb.ListMethodsResponse{}
	err := s.run(ctx, "ListMethods", wickplugin.ScopeHostWhoami, "", func(*call) error {
		for _, m := range wickplugin.HostMethods {
			out.Methods = append(out.Methods, &hostpb.MethodInfo{Name: m.Name, Scope: m.Scope, Summary: m.Summary, Destructive: m.Destructive})
		}
		out.Methods = append(out.Methods, invokeMethods()...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- users.read ----

// ListUsers lists approved users (all with include_unapproved), filtered by
// q against name and email, from a cache refreshed every UsersCacheTTL.
func (s *Server) ListUsers(ctx context.Context, in *hostpb.ListUsersRequest) (*hostpb.ListUsersResponse, error) {
	var out *hostpb.ListUsersResponse
	err := s.run(ctx, "ListUsers", wickplugin.ScopeHostUsersRead, "", func(*call) error {
		if s.users == nil {
			return unavailable("ListUsers")
		}
		snap, err := s.users.load(ctx)
		if err != nil {
			return status.Errorf(codes.Unavailable, "list users: %v", err)
		}
		q := strings.ToLower(strings.TrimSpace(in.GetQ()))
		out = &hostpb.ListUsersResponse{CachedAt: snap.at.UTC().Format(time.RFC3339)}
		for _, u := range snap.users {
			if !u.Approved && !in.GetIncludeUnapproved() {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(u.Name), q) && !strings.Contains(strings.ToLower(u.Email), q) {
				continue
			}
			out.Users = append(out.Users, u)
			if l := in.GetLimit(); l > 0 && len(out.Users) >= int(l) {
				break
			}
		}
		return nil
	})
	return out, err
}

// GetUser returns one user by id.
func (s *Server) GetUser(ctx context.Context, in *hostpb.GetUserRequest) (*hostpb.User, error) {
	return s.getUser(ctx, "GetUser", in.GetId(), "")
}

// GetUserByEmail returns one user by email (case-insensitive).
func (s *Server) GetUserByEmail(ctx context.Context, in *hostpb.GetUserByEmailRequest) (*hostpb.User, error) {
	return s.getUser(ctx, "GetUserByEmail", "", in.GetEmail())
}

func (s *Server) getUser(ctx context.Context, rpc, id, email string) (*hostpb.User, error) {
	var out *hostpb.User
	err := s.run(ctx, rpc, wickplugin.ScopeHostUsersRead, "", func(*call) error {
		if s.users == nil {
			return unavailable(rpc)
		}
		if strings.TrimSpace(id+email) == "" {
			return status.Error(codes.InvalidArgument, "id or email is required")
		}
		u, err := s.users.get(ctx, id, email)
		if err != nil {
			return status.Errorf(codes.Unavailable, "list users: %v", err)
		}
		if u == nil {
			return status.Error(codes.NotFound, "user not found")
		}
		out = u
		return nil
	})
	return out, err
}

// ---- accounts.read ----

// ConnectedAccounts lists the accounts linked to a connector instance. Like
// the in-process Ctx it answers for the instance being executed only: the
// caller must be a connector plugin inside an Execute wick started (its
// call id carries the instance), and instance_id must be that instance.
func (s *Server) ConnectedAccounts(ctx context.Context, in *hostpb.ConnectedAccountsRequest) (*hostpb.ConnectedAccountsResponse, error) {
	var out *hostpb.ConnectedAccountsResponse
	err := s.run(ctx, "ConnectedAccounts", wickplugin.ScopeHostAccountsRead, "", func(c *call) error {
		if s.d.Accounts == nil {
			return unavailable("ConnectedAccounts")
		}
		if strings.TrimSpace(in.GetInstanceId()) == "" {
			return status.Error(codes.InvalidArgument, "instance_id is required")
		}
		if c.caller.Kind != wickplugin.KindConnector || c.plugin.Kind != wickplugin.KindConnector {
			return status.Error(codes.PermissionDenied, "ConnectedAccounts is only available to connector plugins")
		}
		if c.caller.InstanceID == "" {
			return status.Error(codes.FailedPrecondition, "ConnectedAccounts needs the call id of a running Execute")
		}
		if in.GetInstanceId() != c.caller.InstanceID {
			return status.Error(codes.PermissionDenied, "instance_id is not the instance being executed")
		}
		accs, err := s.d.Accounts.ConnectedAccounts(ctx, in.GetInstanceId())
		if err != nil {
			return err
		}
		out = &hostpb.ConnectedAccountsResponse{Accounts: accs}
		return nil
	})
	return out, err
}

// ---- config ----

// GetConfig reads one config value. The plugin's own rows (owner "" or its
// own owner/key) need no scope; another owner's need config.read_other. A
// secret of another owner is reported (found, secret) without its value
// unless config.read_secret allows that declared owner/key. wick_pat is
// never returned to anyone: wick resolves it itself.
func (s *Server) GetConfig(ctx context.Context, in *hostpb.GetConfigRequest) (*hostpb.GetConfigResponse, error) {
	caller, _ := CallerFrom(ctx)
	owner := strings.TrimSpace(in.GetOwner())
	self := owner == "" || owner == caller.PluginKey
	if s.d.Plugins != nil {
		if p, ok := s.d.Plugins.Plugin(caller.PluginKey); ok && owner == p.ConfigOwner {
			self = true
		}
	}
	scope := ""
	if !self {
		scope = wickplugin.ScopeHostConfigReadOther
	}
	var out *hostpb.GetConfigResponse
	err := s.run(ctx, "GetConfig", scope, "", func(c *call) error {
		if s.d.Configs == nil {
			return unavailable("GetConfig")
		}
		if strings.TrimSpace(in.GetKey()) == "" {
			return status.Error(codes.InvalidArgument, "key is required")
		}
		o := owner
		if self {
			if c.plugin.ConfigOwner == "" {
				return status.Error(codes.FailedPrecondition, "this plugin has no config store")
			}
			o = c.plugin.ConfigOwner
		}
		v, found, secret, err := s.d.Configs.Config(ctx, o, in.GetKey())
		if err != nil {
			return status.Errorf(codes.Unavailable, "read config: %v", err)
		}
		withheld := false
		switch {
		case in.GetKey() == PATConfigKey:
			v, secret = "", true
		case secret && !self:
			item := wickplugin.ConfigItem(o, in.GetKey())
			if s.d.Perms.Decide(ctx, c.plugin, wickplugin.ScopeHostConfigReadSecret, item).Allowed {
				c.addNote(wickplugin.ScopeHostConfigReadSecret + " " + item)
			} else {
				// Withheld tells the plugin "not allowed", which a blank
				// value alone cannot: an allowed secret may be empty too.
				v, withheld = "", true
			}
		}
		out = &hostpb.GetConfigResponse{Value: v, Found: found, Secret: secret, Withheld: withheld}
		return nil
	})
	return out, err
}

// ---- sessions ----

// ListSessions lists session metadata the acting user can see.
func (s *Server) ListSessions(ctx context.Context, in *hostpb.ListSessionsRequest) (*hostpb.ListSessionsResponse, error) {
	var out *hostpb.ListSessionsResponse
	err := s.run(ctx, "ListSessions", wickplugin.ScopeHostSessionsRead, "", func(c *call) error {
		if s.d.Sessions == nil {
			return unavailable("ListSessions")
		}
		list, err := s.d.Sessions.ListSessions(ctx, c.userID, in)
		if err != nil {
			return err
		}
		out = &hostpb.ListSessionsResponse{Sessions: list}
		return nil
	})
	return out, err
}

// GetSessionMeta returns one session's metadata.
func (s *Server) GetSessionMeta(ctx context.Context, in *hostpb.GetSessionMetaRequest) (*hostpb.SessionMeta, error) {
	var out *hostpb.SessionMeta
	err := s.run(ctx, "GetSessionMeta", wickplugin.ScopeHostSessionsRead, "", func(c *call) error {
		if s.d.Sessions == nil {
			return unavailable("GetSessionMeta")
		}
		if strings.TrimSpace(in.GetId()) == "" {
			return status.Error(codes.InvalidArgument, "id is required")
		}
		var err error
		out, err = s.d.Sessions.GetSessionMeta(ctx, c.userID, in.GetId())
		return err
	})
	return out, err
}

// SendToSession queues a message into a session as the acting user.
func (s *Server) SendToSession(ctx context.Context, in *hostpb.SendToSessionRequest) (*hostpb.SendToSessionResponse, error) {
	var out *hostpb.SendToSessionResponse
	err := s.run(ctx, "SendToSession", wickplugin.ScopeHostSessionsNotify, "", func(c *call) error {
		if s.d.Sessions == nil {
			return unavailable("SendToSession")
		}
		if strings.TrimSpace(in.GetSessionId()) == "" || strings.TrimSpace(in.GetText()) == "" {
			return status.Error(codes.InvalidArgument, "session_id and text are required")
		}
		queued, err := s.d.Sessions.SendToSession(ctx, c.userID, in.GetSessionId(), in.GetText())
		if err != nil {
			return err
		}
		out = &hostpb.SendToSessionResponse{Queued: queued}
		return nil
	})
	return out, err
}

// ---- tickets ----

func (s *Server) tickets(rpc string) (TicketService, error) {
	if s.d.Tickets == nil {
		return nil, unavailable(rpc)
	}
	return s.d.Tickets, nil
}

// GetTicket returns one ticket the acting user can see.
func (s *Server) GetTicket(ctx context.Context, in *hostpb.GetTicketRequest) (*hostpb.Ticket, error) {
	var out *hostpb.Ticket
	err := s.run(ctx, "GetTicket", wickplugin.ScopeHostTicketsRead, "", func(c *call) error {
		t, err := s.tickets("GetTicket")
		if err != nil {
			return err
		}
		if strings.TrimSpace(in.GetId()) == "" {
			return status.Error(codes.InvalidArgument, "id is required")
		}
		out, err = t.GetTicket(ctx, c.userID, in)
		return err
	})
	return out, err
}

// ListTickets lists a project's tickets.
func (s *Server) ListTickets(ctx context.Context, in *hostpb.ListTicketsRequest) (*hostpb.ListTicketsResponse, error) {
	var out *hostpb.ListTicketsResponse
	err := s.run(ctx, "ListTickets", wickplugin.ScopeHostTicketsRead, "", func(c *call) error {
		t, err := s.tickets("ListTickets")
		if err != nil {
			return err
		}
		list, err := t.ListTickets(ctx, c.userID, in)
		if err != nil {
			return err
		}
		out = &hostpb.ListTicketsResponse{Tickets: list}
		return nil
	})
	return out, err
}

// CreateTicket creates a ticket as the acting user.
func (s *Server) CreateTicket(ctx context.Context, in *hostpb.CreateTicketRequest) (*hostpb.Ticket, error) {
	var out *hostpb.Ticket
	err := s.run(ctx, "CreateTicket", wickplugin.ScopeHostTicketsWrite, "", func(c *call) error {
		t, err := s.tickets("CreateTicket")
		if err != nil {
			return err
		}
		out, err = t.CreateTicket(ctx, c.userID, in)
		return err
	})
	return out, err
}

// UpdateTicket updates a ticket as the acting user.
func (s *Server) UpdateTicket(ctx context.Context, in *hostpb.UpdateTicketRequest) (*hostpb.Ticket, error) {
	var out *hostpb.Ticket
	err := s.run(ctx, "UpdateTicket", wickplugin.ScopeHostTicketsWrite, "", func(c *call) error {
		t, err := s.tickets("UpdateTicket")
		if err != nil {
			return err
		}
		if strings.TrimSpace(in.GetId()) == "" {
			return status.Error(codes.InvalidArgument, "id is required")
		}
		out, err = t.UpdateTicket(ctx, c.userID, in)
		return err
	})
	return out, err
}

// AttachSession attaches a session to a ticket as the acting user.
func (s *Server) AttachSession(ctx context.Context, in *hostpb.AttachSessionRequest) (*hostpb.Empty, error) {
	err := s.run(ctx, "AttachSession", wickplugin.ScopeHostTicketsWrite, "", func(c *call) error {
		t, err := s.tickets("AttachSession")
		if err != nil {
			return err
		}
		if strings.TrimSpace(in.GetTicketId()) == "" || strings.TrimSpace(in.GetSessionId()) == "" {
			return status.Error(codes.InvalidArgument, "ticket_id and session_id are required")
		}
		return t.AttachSession(ctx, c.userID, in)
	})
	if err != nil {
		return nil, err
	}
	return &hostpb.Empty{}, nil
}
