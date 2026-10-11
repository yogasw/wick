// Package host is the plugin-side client of wick's Host API (wick.host.v1):
// the calls a plugin makes back into wick (users, sessions, tickets, connector
// instances, ...). One Host interface sits over two transports:
//
//   - gRPC over the go-plugin GRPCBroker connection (connector/tool/job), see
//     FromConn;
//   - HTTP to /x/-/api/* with WICK_PLUGIN_TOKEN (every kind, service plugins
//     included), see FromEnv.
//
// Default picks whichever is available. Nothing here panics on a wick that
// predates the Host API: with no broker connection and no plugin token every
// constructor returns ErrHostAPIUnavailable. A service plugin on such a wick
// does hold a token, so the constructors succeed and each RPC then returns
// ErrHostAPIUnavailable; Whoami alone succeeds over HTTP with HostApiVersion 0
// (the callback door's original whoami). Probe Whoami and treat version 0 as
// "no Host API" when that matters.
//
// The package deliberately imports nothing from pkg/plugin so the SDK there
// can depend on it.
package host

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"

	"google.golang.org/grpc"

	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// APIVersion is the Host API version this client speaks (wick.host.v1).
const APIVersion = 1

// Env the host sets on a plugin process for the HTTP door. EnvBaseURL and
// EnvPluginToken match pkg/plugin.EnvBaseURL / EnvPluginToken (kept in sync by
// a test).
//
// EnvHostAPIURL is the private (loopback) address of the Host API door. It
// wins over EnvBaseURL, which is wick's public URL; EnvBaseURL is only the
// fallback for a wick that sets no EnvHostAPIURL.
const (
	EnvHostAPIURL  = "WICK_HOST_API_URL"
	EnvBaseURL     = "WICK_BASE_URL"
	EnvPluginToken = "WICK_PLUGIN_TOKEN"
)

// Host is wick's Host API as seen from a plugin. Every method takes the
// request message of its RPC (nil = empty request) so adding a field to the
// proto never changes a signature.
type Host interface {
	// meta (scope: whoami, always allowed)
	Whoami(ctx context.Context) (*hostpb.WhoamiResponse, error)
	ListMethods(ctx context.Context) (*hostpb.ListMethodsResponse, error)

	// users.read
	ListUsers(ctx context.Context, in *hostpb.ListUsersRequest) (*hostpb.ListUsersResponse, error)
	GetUser(ctx context.Context, in *hostpb.GetUserRequest) (*hostpb.User, error)
	GetUserByEmail(ctx context.Context, in *hostpb.GetUserByEmailRequest) (*hostpb.User, error)

	// accounts.read
	ConnectedAccounts(ctx context.Context, in *hostpb.ConnectedAccountsRequest) (*hostpb.ConnectedAccountsResponse, error)

	// sessions.read / sessions.notify
	ListSessions(ctx context.Context, in *hostpb.ListSessionsRequest) (*hostpb.ListSessionsResponse, error)
	GetSessionMeta(ctx context.Context, in *hostpb.GetSessionMetaRequest) (*hostpb.SessionMeta, error)
	SendToSession(ctx context.Context, in *hostpb.SendToSessionRequest) (*hostpb.SendToSessionResponse, error)

	// tickets.read / tickets.write
	GetTicket(ctx context.Context, in *hostpb.GetTicketRequest) (*hostpb.Ticket, error)
	ListTickets(ctx context.Context, in *hostpb.ListTicketsRequest) (*hostpb.ListTicketsResponse, error)
	CreateTicket(ctx context.Context, in *hostpb.CreateTicketRequest) (*hostpb.Ticket, error)
	UpdateTicket(ctx context.Context, in *hostpb.UpdateTicketRequest) (*hostpb.Ticket, error)
	AttachSession(ctx context.Context, in *hostpb.AttachSessionRequest) error

	// config.read_other
	GetConfig(ctx context.Context, in *hostpb.GetConfigRequest) (*hostpb.GetConfigResponse, error)

	// connectors.read / connectors.execute
	ListConnectorInstances(ctx context.Context, in *hostpb.ListConnectorInstancesRequest) (*hostpb.ListConnectorInstancesResponse, error)
	ExecuteConnector(ctx context.Context, in *hostpb.ExecuteConnectorRequest) (*hostpb.ExecuteConnectorResponse, error)

	// generic layer
	Invoke(ctx context.Context, in *hostpb.InvokeRequest) (*hostpb.InvokeResponse, error)
	Rest(ctx context.Context, in *hostpb.RestRequest) (*hostpb.RestResponse, error)
	Query(ctx context.Context, in *hostpb.QueryRequest) (*hostpb.QueryResponse, error)
}

// FromConn returns a Host speaking gRPC over conn, the connection the
// GRPCBroker hands the plugin. A nil conn (no broker id in the handshake, i.e.
// an older wick) yields ErrHostAPIUnavailable.
func FromConn(conn grpc.ClientConnInterface) (Host, error) {
	if conn == nil {
		return nil, ErrHostAPIUnavailable
	}
	return &client{t: &grpcTransport{cc: conn}}, nil
}

// FromEnv returns a Host speaking HTTP to WICK_HOST_API_URL (else
// WICK_BASE_URL) with WICK_PLUGIN_TOKEN. No URL or no token yields
// ErrHostAPIUnavailable.
func FromEnv() (Host, error) {
	base := strings.TrimSpace(os.Getenv(EnvHostAPIURL))
	if base == "" {
		base = os.Getenv(EnvBaseURL)
	}
	return FromHTTP(base, os.Getenv(EnvPluginToken), nil)
}

// FromHTTP is FromEnv with explicit values. hc nil uses a client that never
// follows redirects (a 3xx from Rest reaches the caller as its status); the
// per-call deadline comes from the context (see DefaultCallTimeout).
func FromHTTP(baseURL, token string, hc *http.Client) (Host, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, ErrHostAPIUnavailable
	}
	if hc == nil {
		hc = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &client{t: &httpTransport{base: baseURL, token: token, hc: hc}}, nil
}

var (
	connMu sync.RWMutex
	conn   grpc.ClientConnInterface
)

// SetConn records the broker connection for Default. The plugin SDK calls it
// once the host announces a Host API broker id; plugins do not.
func SetConn(c grpc.ClientConnInterface) {
	connMu.Lock()
	conn = c
	connMu.Unlock()
}

// Default picks a transport by itself: the broker connection when the SDK
// registered one, otherwise the HTTP env. Neither available (an older wick, or
// a process not started by wick) yields ErrHostAPIUnavailable.
func Default() (Host, error) {
	connMu.RLock()
	c := conn
	connMu.RUnlock()
	if c != nil {
		return FromConn(c)
	}
	return FromEnv()
}
