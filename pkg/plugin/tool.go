package plugin

import (
	"context"
	"fmt"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
)

// ToolPluginName is the dispense key for tool plugins.
const ToolPluginName = "tool"

// EnvToolSocket names the unix socket a tool plugin must serve HTTP on. The
// host picks the path (inside its 0700 run dir) and passes it at spawn; the
// plugin creates it 0600 before the go-plugin handshake, so a finished
// handshake means the socket already accepts requests.
const EnvToolSocket = "WICK_PLUGIN_SOCKET"

// Headers the host sets on every proxied tool request. Any client-sent value
// is dropped before the host writes its own, so a plugin can trust them.
const (
	HeaderUserID    = "X-Wick-User-Id"
	HeaderUserEmail = "X-Wick-User-Email"
	HeaderUserName  = "X-Wick-User-Name"
	HeaderUserRole  = "X-Wick-User-Role" // "admin" or "user"; absent = no session
	HeaderUserTags  = "X-Wick-User-Tags" // comma-separated tag names
	HeaderBase      = "X-Wick-Base"      // "/tools/{key}"
	// HeaderLayout on a plugin response asks the host to wrap the body (an
	// HTML fragment) in wick's page layout; HeaderTitle names the page.
	HeaderLayout = "X-Wick-Layout"
	HeaderTitle  = "X-Wick-Title"
	// LayoutPage is the HeaderLayout value for a full wick page.
	LayoutPage = "page"
	// HeaderPrefix is the namespace the host strips from client requests.
	HeaderPrefix = "X-Wick-"
)

// ToolMeta is the manifest form of tool.Tool. It is a plain copy (not
// tool.Tool itself) so pkg/plugin — linked into every connector binary —
// does not pull in pkg/tool and its templ render stack.
type ToolMeta struct {
	Key         string              `json:"key"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Icon        string              `json:"icon,omitempty"`
	Category    string              `json:"category,omitempty"`
	ExternalURL string              `json:"external_url,omitempty"`
	Visibility  string              `json:"visibility,omitempty"` // entity.ToolVisibility; "" = private
	FullScreen  bool                `json:"full_screen,omitempty"`
	DefaultTags []entity.DefaultTag `json:"default_tags,omitempty"`
}

// ToolWebhook is one route a tool plugin opened with Router.WebhookGroup.
// Paths are relative to /tools/{key}. Only these exact METHOD+path pairs are
// reachable without a wick session; anything else under Group stays 404.
type ToolWebhook struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Group  string `json:"group"`
}

// ToolModule is the tool half of a kind=tool manifest.
type ToolModule struct {
	Meta     ToolMeta        `json:"meta"`
	Configs  []entity.Config `json:"configs,omitempty"`
	Webhooks []ToolWebhook   `json:"webhooks,omitempty"`
	// KeepWarm keeps the process running instead of idle-killing it — for
	// tools whose webhook must answer fast (Slack's 3 s trigger_id window).
	KeepWarm bool `json:"keep_warm,omitempty"`
}

// ToolGRPCPlugin is the go-plugin descriptor for the Tool control service.
// Impl is set only on the plugin side.
type ToolGRPCPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl pb.ToolServer
}

func (p *ToolGRPCPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterToolServer(s, p.Impl)
	return nil
}

func (p *ToolGRPCPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &toolClient{inner: pb.NewToolClient(c)}, nil
}

// ToolVersionedPlugins is the host-side plugin set for tool plugins.
var ToolVersionedPlugins = func() map[int]goplugin.PluginSet {
	out := map[int]goplugin.PluginSet{}
	for _, v := range SupportedProtoVersions() {
		out[v] = goplugin.PluginSet{ToolPluginName: &ToolGRPCPlugin{}}
	}
	return out
}()

// BuildSelfToolManifest is BuildSelfManifest for a tool binary: kind=tool,
// the tool half in Tool, and Module.Meta mirroring key/name/description/icon
// so the shared install/scan code keeps working unchanged.
func BuildSelfToolManifest(tm ToolModule, signKeyPath string) (Manifest, error) {
	m, err := BuildSelfManifest(connector.Module{Meta: connector.Meta{
		Key:         tm.Meta.Key,
		Name:        tm.Meta.Name,
		Description: tm.Meta.Description,
		Icon:        tm.Meta.Icon,
	}}, signKeyPath)
	if err != nil {
		return Manifest{}, err
	}
	m.Kind = KindTool
	m.Tool = &tm
	return m, nil
}

// ApplyRlimits applies the plugin-process resource limits (the same ones
// ServeJob and the connector Serve apply) for plugin mains that live outside
// this package, such as toolplugin.ServeTool.
func ApplyRlimits() { applyRlimits() }

// ── host side ──────────────────────────────────────────────────────────

// ToolConn is the host-facing control surface of a running tool plugin.
type ToolConn interface {
	Configure(ctx context.Context, cfg map[string]string) error
	Health(ctx context.Context) error
}

type toolClient struct{ inner pb.ToolClient }

func (c *toolClient) Configure(ctx context.Context, cfg map[string]string) error {
	resp, err := c.inner.Configure(ctx, &pb.ExecuteRequest{Creds: cfg})
	if err != nil {
		return err
	}
	if !resp.Healthy {
		return fmt.Errorf("tool plugin rejected config: %s", resp.Detail)
	}
	return nil
}

func (c *toolClient) Health(ctx context.Context) error {
	resp, err := c.inner.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		return err
	}
	if !resp.Healthy {
		return fmt.Errorf("tool plugin unhealthy: %s", resp.Detail)
	}
	return nil
}
