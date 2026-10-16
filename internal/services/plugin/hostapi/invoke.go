package hostapi

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/plugin/host"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// InvokeCall is what an Invoke method handler runs with.
type InvokeCall struct {
	PluginKey string
	// UserID is the acting person; "" for a method whose scope is
	// plugin-level.
	UserID string
	Args   json.RawMessage
}

// InvokeHandler serves one method registered with RegisterMethod. It
// returns the JSON result.
type InvokeHandler func(ctx context.Context, in InvokeCall) (json.RawMessage, error)

type invokeMethod struct {
	info      *hostpb.MethodInfo
	userLevel bool
	h         InvokeHandler
}

var (
	methodsMu sync.RWMutex
	methods   = map[string]invokeMethod{}
)

// RegisterMethod makes name callable through Invoke under scope (an official
// RPC scope without a host_resources block). The method runs as the acting
// person when a user-level RPC uses the same scope (sessions, tickets,
// connectors.read), else as the plugin alone. Its admin item is the method
// name, so an admin can block one method and keep the rest of the scope.
// It panics on an unknown, declarative or resource scope, an empty name or
// a name taken by a built-in alias or another registration: programming
// errors caught at start-up.
func RegisterMethod(name, scope string, destructive bool, h InvokeHandler) {
	sc, ok := wickplugin.LookupHostScope(scope)
	if name == "" || h == nil || !ok || sc.Declarative || sc.Resource != "" || scope == wickplugin.ScopeHostWhoami {
		panic(fmt.Sprintf("hostapi: RegisterMethod(%q, %q): bad name, scope or handler", name, scope))
	}
	if _, taken := builtinInvoke[name]; taken {
		panic("hostapi: RegisterMethod: " + name + " is a built-in method")
	}
	methodsMu.Lock()
	defer methodsMu.Unlock()
	if _, taken := methods[name]; taken {
		panic("hostapi: RegisterMethod: " + name + " registered twice")
	}
	methods[name] = invokeMethod{
		info:      &hostpb.MethodInfo{Name: name, Scope: scope, Destructive: destructive || sc.Destructive},
		userLevel: scopeIsUserLevel(scope),
		h:         h,
	}
}

// unregisterMethod removes name (tests).
func unregisterMethod(name string) {
	methodsMu.Lock()
	delete(methods, name)
	methodsMu.Unlock()
}

func lookupMethod(name string) (invokeMethod, bool) {
	methodsMu.RLock()
	defer methodsMu.RUnlock()
	m, ok := methods[name]
	return m, ok
}

// scopeIsUserLevel reports whether any Host RPC under scope is user-level.
func scopeIsUserLevel(scope string) bool {
	for _, m := range wickplugin.HostMethods {
		if m.Scope == scope && m.UserLevel {
			return true
		}
	}
	return false
}

// builtinAlias exposes a Host RPC under an Invoke name. The call goes
// through the RPC itself, so its scope, identity and audit row are the
// RPC's own.
type builtinAlias struct {
	rpc string
	in  func() proto.Message
	fn  func(s *Server) rpcFunc
}

// builtinInvoke are the Invoke names of existing RPCs.
var builtinInvoke = map[string]builtinAlias{
	"users.list": {"ListUsers", func() proto.Message { return &hostpb.ListUsersRequest{} },
		func(s *Server) rpcFunc { return bind(s.ListUsers) }},
	"users.get": {"GetUser", func() proto.Message { return &hostpb.GetUserRequest{} },
		func(s *Server) rpcFunc { return bind(s.GetUser) }},
	"sessions.list": {"ListSessions", func() proto.Message { return &hostpb.ListSessionsRequest{} },
		func(s *Server) rpcFunc { return bind(s.ListSessions) }},
	"sessions.get": {"GetSessionMeta", func() proto.Message { return &hostpb.GetSessionMetaRequest{} },
		func(s *Server) rpcFunc { return bind(s.GetSessionMeta) }},
	"tickets.list": {"ListTickets", func() proto.Message { return &hostpb.ListTicketsRequest{} },
		func(s *Server) rpcFunc { return bind(s.ListTickets) }},
	"tickets.get": {"GetTicket", func() proto.Message { return &hostpb.GetTicketRequest{} },
		func(s *Server) rpcFunc { return bind(s.GetTicket) }},
	"connectors.list": {"ListConnectorInstances", func() proto.Message { return &hostpb.ListConnectorInstancesRequest{} },
		func(s *Server) rpcFunc { return bind(s.ListConnectorInstances) }},
}

// invokeMethods lists the built-in aliases and the registered methods, by
// name.
func invokeMethods() []*hostpb.MethodInfo {
	var out []*hostpb.MethodInfo
	for name, a := range builtinInvoke {
		m, _ := wickplugin.LookupHostMethod(a.rpc)
		out = append(out, &hostpb.MethodInfo{Name: name, Scope: m.Scope, Summary: "Invoke alias of " + a.rpc + ".", Destructive: m.Destructive})
	}
	methodsMu.RLock()
	for _, m := range methods {
		out = append(out, proto.Clone(m.info).(*hostpb.MethodInfo))
	}
	methodsMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Invoke calls a method by name with the scope of that method. Built-in
// aliases run their RPC (its own gate and audit row); registered methods
// are gated and audited here with the method name as the item.
func (s *Server) Invoke(ctx context.Context, in *hostpb.InvokeRequest) (*hostpb.InvokeResponse, error) {
	name := in.GetMethod()
	args := in.GetArgsJson()
	if a, ok := builtinInvoke[name]; ok {
		req := a.in()
		if len(args) > 0 {
			if err := host.UnmarshalHTTP(args, req); err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "args for %s: %v", name, err)
			}
		}
		resp, err := a.fn(s)(ctx, req)
		if err != nil {
			return nil, err
		}
		body, err := host.MarshalHTTP(resp)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		return &hostpb.InvokeResponse{ResultJson: body}, nil
	}
	m, ok := lookupMethod(name)
	if !ok {
		err := s.run(ctx, "Invoke", "", name, func(*call) error {
			return status.Errorf(codes.NotFound, "no Host API method %q (see ListMethods)", name)
		})
		return nil, err
	}
	var out *hostpb.InvokeResponse
	err := s.runGate(ctx, gate{rpc: "Invoke", scope: m.info.GetScope(), item: name, userLevel: m.userLevel}, func(c *call) error {
		if len(args) > 0 && !json.Valid(args) {
			return status.Error(codes.InvalidArgument, "args must be JSON")
		}
		res, err := m.h(ctx, InvokeCall{PluginKey: c.plugin.Key, UserID: c.userID, Args: json.RawMessage(args)})
		if err != nil {
			return err
		}
		if len(res) == 0 {
			res = json.RawMessage("null")
		}
		if !json.Valid(res) {
			return status.Errorf(codes.Internal, "method %s returned invalid JSON", name)
		}
		out = &hostpb.InvokeResponse{ResultJson: res}
		return nil
	})
	return out, err
}
