package teamlink

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// Protocol is the in-process transport binding a local Team agent's card
// advertises.
const Protocol a2a.TransportProtocol = "wick-local"

// localScheme prefixes a local agent's interface URL.
const localScheme = "wick-local://team/"

// LocalURL is the interface URL of a local Team agent.
func LocalURL(agentID string) string { return localScheme + agentID }

// createTransport is the a2aclient.TransportFactory for Protocol.
func (h *Hub) createTransport(_ context.Context, _ *a2a.AgentCard, iface *a2a.AgentInterface) (a2aclient.Transport, error) {
	id, ok := strings.CutPrefix(iface.URL, localScheme)
	if !ok || id == "" {
		return nil, fmt.Errorf("teamlink: not a local agent url: %q", iface.URL)
	}
	return &localTransport{rh: h.handler(id)}, nil
}

// localTransport calls a RequestHandler directly: the client side of A2A
// with the network taken out.
type localTransport struct {
	rh a2asrv.RequestHandler
}

var _ a2aclient.Transport = (*localTransport)(nil)

// call attaches the client's service params the way a server transport
// would before handing the request on.
func call(ctx context.Context, p a2aclient.ServiceParams) context.Context {
	ctx, _ = a2asrv.NewCallContext(ctx, a2asrv.NewServiceParams(p))
	return ctx
}

func (t *localTransport) GetTask(ctx context.Context, p a2aclient.ServiceParams, r *a2a.GetTaskRequest) (*a2a.Task, error) {
	return t.rh.GetTask(call(ctx, p), r)
}

func (t *localTransport) ListTasks(ctx context.Context, p a2aclient.ServiceParams, r *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return t.rh.ListTasks(call(ctx, p), r)
}

func (t *localTransport) CancelTask(ctx context.Context, p a2aclient.ServiceParams, r *a2a.CancelTaskRequest) (*a2a.Task, error) {
	return t.rh.CancelTask(call(ctx, p), r)
}

func (t *localTransport) SendMessage(ctx context.Context, p a2aclient.ServiceParams, r *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	return t.rh.SendMessage(call(ctx, p), r)
}

func (t *localTransport) SubscribeToTask(ctx context.Context, p a2aclient.ServiceParams, r *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	return t.rh.SubscribeToTask(call(ctx, p), r)
}

func (t *localTransport) SendStreamingMessage(ctx context.Context, p a2aclient.ServiceParams, r *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return t.rh.SendStreamingMessage(call(ctx, p), r)
}

func (t *localTransport) GetTaskPushConfig(ctx context.Context, p a2aclient.ServiceParams, r *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return t.rh.GetTaskPushConfig(call(ctx, p), r)
}

func (t *localTransport) ListTaskPushConfigs(ctx context.Context, p a2aclient.ServiceParams, r *a2a.ListTaskPushConfigRequest) ([]*a2a.PushConfig, error) {
	res, err := t.rh.ListTaskPushConfigs(call(ctx, p), r)
	if err != nil || res == nil {
		return nil, err
	}
	return res.Configs, nil
}

func (t *localTransport) CreateTaskPushConfig(ctx context.Context, p a2aclient.ServiceParams, r *a2a.PushConfig) (*a2a.PushConfig, error) {
	return t.rh.CreateTaskPushConfig(call(ctx, p), r)
}

func (t *localTransport) DeleteTaskPushConfig(ctx context.Context, p a2aclient.ServiceParams, r *a2a.DeleteTaskPushConfigRequest) error {
	return t.rh.DeleteTaskPushConfig(call(ctx, p), r)
}

func (t *localTransport) GetExtendedAgentCard(ctx context.Context, p a2aclient.ServiceParams, r *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return t.rh.GetExtendedAgentCard(call(ctx, p), r)
}

// Destroy has nothing to release: there is no connection.
func (t *localTransport) Destroy() error { return nil }
