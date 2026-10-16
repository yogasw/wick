package host

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// transport carries one RPC; rpc is the method name in the Host service.
type transport interface {
	call(ctx context.Context, rpc string, in, out proto.Message) error
}

type client struct{ t transport }

func invoke[Out any, PO interface {
	*Out
	proto.Message
}](ctx context.Context, t transport, rpc string, in proto.Message) (*Out, error) {
	out := PO(new(Out))
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout(rpc, in))
		defer cancel()
	}
	if err := t.call(ctx, rpc, in, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Per-call deadlines applied when the caller's ctx has none, the same on both
// transports.
const (
	DefaultCallTimeout   = 30 * time.Second
	ExecuteCallTimeout   = 3 * time.Minute // connector ops may be slow
	queryTimeoutHeadroom = 5 * time.Second // let the server's statement_timeout fire first
)

func callTimeout(rpc string, in proto.Message) time.Duration {
	switch rpc {
	case "ExecuteConnector":
		return ExecuteCallTimeout
	case "Query":
		if q, ok := in.(*hostpb.QueryRequest); ok && q.GetTimeoutMs() > 0 {
			return time.Duration(q.GetTimeoutMs())*time.Millisecond + queryTimeoutHeadroom
		}
	}
	return DefaultCallTimeout
}

// orEmpty returns in, or a fresh zero request when in is nil.
func orEmpty[T any](in *T) *T {
	if in == nil {
		return new(T)
	}
	return in
}

func (c *client) Whoami(ctx context.Context) (*hostpb.WhoamiResponse, error) {
	return invoke[hostpb.WhoamiResponse](ctx, c.t, "Whoami", &hostpb.WhoamiRequest{})
}

func (c *client) ListMethods(ctx context.Context) (*hostpb.ListMethodsResponse, error) {
	return invoke[hostpb.ListMethodsResponse](ctx, c.t, "ListMethods", &hostpb.ListMethodsRequest{})
}

func (c *client) ListUsers(ctx context.Context, in *hostpb.ListUsersRequest) (*hostpb.ListUsersResponse, error) {
	return invoke[hostpb.ListUsersResponse](ctx, c.t, "ListUsers", orEmpty(in))
}

func (c *client) GetUser(ctx context.Context, in *hostpb.GetUserRequest) (*hostpb.User, error) {
	return invoke[hostpb.User](ctx, c.t, "GetUser", orEmpty(in))
}

func (c *client) GetUserByEmail(ctx context.Context, in *hostpb.GetUserByEmailRequest) (*hostpb.User, error) {
	return invoke[hostpb.User](ctx, c.t, "GetUserByEmail", orEmpty(in))
}

func (c *client) ConnectedAccounts(ctx context.Context, in *hostpb.ConnectedAccountsRequest) (*hostpb.ConnectedAccountsResponse, error) {
	return invoke[hostpb.ConnectedAccountsResponse](ctx, c.t, "ConnectedAccounts", orEmpty(in))
}

func (c *client) ListSessions(ctx context.Context, in *hostpb.ListSessionsRequest) (*hostpb.ListSessionsResponse, error) {
	return invoke[hostpb.ListSessionsResponse](ctx, c.t, "ListSessions", orEmpty(in))
}

func (c *client) GetSessionMeta(ctx context.Context, in *hostpb.GetSessionMetaRequest) (*hostpb.SessionMeta, error) {
	return invoke[hostpb.SessionMeta](ctx, c.t, "GetSessionMeta", orEmpty(in))
}

func (c *client) SendToSession(ctx context.Context, in *hostpb.SendToSessionRequest) (*hostpb.SendToSessionResponse, error) {
	return invoke[hostpb.SendToSessionResponse](ctx, c.t, "SendToSession", orEmpty(in))
}

func (c *client) GetTicket(ctx context.Context, in *hostpb.GetTicketRequest) (*hostpb.Ticket, error) {
	return invoke[hostpb.Ticket](ctx, c.t, "GetTicket", orEmpty(in))
}

func (c *client) ListTickets(ctx context.Context, in *hostpb.ListTicketsRequest) (*hostpb.ListTicketsResponse, error) {
	return invoke[hostpb.ListTicketsResponse](ctx, c.t, "ListTickets", orEmpty(in))
}

func (c *client) CreateTicket(ctx context.Context, in *hostpb.CreateTicketRequest) (*hostpb.Ticket, error) {
	return invoke[hostpb.Ticket](ctx, c.t, "CreateTicket", orEmpty(in))
}

func (c *client) UpdateTicket(ctx context.Context, in *hostpb.UpdateTicketRequest) (*hostpb.Ticket, error) {
	return invoke[hostpb.Ticket](ctx, c.t, "UpdateTicket", orEmpty(in))
}

func (c *client) AttachSession(ctx context.Context, in *hostpb.AttachSessionRequest) error {
	_, err := invoke[hostpb.Empty](ctx, c.t, "AttachSession", orEmpty(in))
	return err
}

func (c *client) GetConfig(ctx context.Context, in *hostpb.GetConfigRequest) (*hostpb.GetConfigResponse, error) {
	return invoke[hostpb.GetConfigResponse](ctx, c.t, "GetConfig", orEmpty(in))
}

func (c *client) ListConnectorInstances(ctx context.Context, in *hostpb.ListConnectorInstancesRequest) (*hostpb.ListConnectorInstancesResponse, error) {
	return invoke[hostpb.ListConnectorInstancesResponse](ctx, c.t, "ListConnectorInstances", orEmpty(in))
}

func (c *client) ExecuteConnector(ctx context.Context, in *hostpb.ExecuteConnectorRequest) (*hostpb.ExecuteConnectorResponse, error) {
	return invoke[hostpb.ExecuteConnectorResponse](ctx, c.t, "ExecuteConnector", orEmpty(in))
}

func (c *client) Invoke(ctx context.Context, in *hostpb.InvokeRequest) (*hostpb.InvokeResponse, error) {
	return invoke[hostpb.InvokeResponse](ctx, c.t, "Invoke", orEmpty(in))
}

func (c *client) Rest(ctx context.Context, in *hostpb.RestRequest) (*hostpb.RestResponse, error) {
	return invoke[hostpb.RestResponse](ctx, c.t, "Rest", orEmpty(in))
}

func (c *client) Query(ctx context.Context, in *hostpb.QueryRequest) (*hostpb.QueryResponse, error) {
	return invoke[hostpb.QueryResponse](ctx, c.t, "Query", orEmpty(in))
}
