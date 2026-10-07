// Package toolplugin is the plugin side of tool plugins: ServeTool turns an
// ordinary tool.Module (the same Register func a built-in tool uses) into a
// binary that serves HTTP on the unix socket the host names, while the host
// reverse-proxies /tools/{key}/* to it and wraps page fragments in wick's
// layout.
//
// It lives apart from pkg/plugin so connector binaries, which import
// pkg/plugin, do not link pkg/tool and its templ render stack.
package toolplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
	"github.com/yogasw/wick/pkg/tool"
)

// Option tunes a tool plugin's manifest.
type Option func(*wickplugin.ToolModule)

// KeepWarm marks the tool to stay running instead of being idle-killed. Use
// it for tools whose webhook must answer within a few seconds.
func KeepWarm() Option { return func(m *wickplugin.ToolModule) { m.KeepWarm = true } }

// cfgStore is the tool's config as last pushed by the host (Configure).
type cfgStore struct {
	mu       sync.RWMutex
	key      string
	vals     map[string]string
	required []string
}

func (s *cfgStore) GetOwned(owner, key string) string {
	if owner != s.key {
		return "" // other owners' config never leaves the host
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vals[key]
}

func (s *cfgStore) Missing(owner string) []string {
	if owner != s.key {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for _, k := range s.required {
		if strings.TrimSpace(s.vals[k]) == "" {
			out = append(out, k)
		}
	}
	return out
}

func (s *cfgStore) set(v map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vals = v
}

// UserFromHeaders reads the signed-in user the host injected. ok is false
// when the request carries no session (webhooks, public tools).
func UserFromHeaders(r *http.Request) (tool.User, bool) {
	id := r.Header.Get(wickplugin.HeaderUserID)
	if id == "" {
		return tool.User{}, false
	}
	u := tool.User{
		ID:      id,
		Name:    r.Header.Get(wickplugin.HeaderUserName),
		Email:   r.Header.Get(wickplugin.HeaderUserEmail),
		IsAdmin: r.Header.Get(wickplugin.HeaderUserRole) == "admin",
	}
	for _, t := range strings.Split(r.Header.Get(wickplugin.HeaderUserTags), ",") {
		if t = strings.TrimSpace(t); t != "" {
			u.Tags = append(u.Tags, t)
		}
	}
	return u, true
}

type toolServer struct {
	pb.UnimplementedToolServer
	schema []byte
	cfg    *cfgStore
}

func (s *toolServer) Schema(context.Context, *pb.SchemaRequest) (*pb.SchemaResponse, error) {
	return &pb.SchemaResponse{ManifestJson: s.schema}, nil
}

func (s *toolServer) Configure(_ context.Context, req *pb.ExecuteRequest) (*pb.HealthResponse, error) {
	s.cfg.set(req.Creds)
	return &pb.HealthResponse{Healthy: true}, nil
}

func (s *toolServer) Health(context.Context, *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Healthy: true}, nil
}

// build registers mod on a plugin router and derives the manifest half.
func build(mod tool.Module, opts []Option) (*router, *cfgStore, wickplugin.ToolModule) {
	store := &cfgStore{key: mod.Meta.Key, vals: map[string]string{}}
	for _, c := range mod.Configs {
		if c.Required {
			store.required = append(store.required, c.Key)
		}
	}
	r := newRouter(mod.Meta, store)
	if mod.Register != nil {
		mod.Register(r)
	}
	tm := wickplugin.ToolModule{
		Meta: wickplugin.ToolMeta{
			Key:         mod.Meta.Key,
			Name:        mod.Meta.Name,
			Description: mod.Meta.Description,
			Icon:        mod.Meta.Icon,
			Category:    mod.Meta.Category,
			ExternalURL: mod.Meta.ExternalURL,
			Visibility:  string(mod.Meta.DefaultVisibility),
			FullScreen:  mod.Meta.FullScreen,
			DefaultTags: mod.Meta.DefaultTags,
			Replaces:    mod.Meta.Replaces,
		},
		Configs:  mod.Configs,
		Webhooks: r.webhooks(),
	}
	for _, o := range opts {
		o(&tm)
	}
	return r, store, tm
}

// Handler returns the plugin's HTTP handler for mod without serving it —
// for tests and local harnesses. cfg seeds the config store.
func Handler(mod tool.Module, cfg map[string]string) http.Handler {
	r, store, _ := build(mod, nil)
	if cfg != nil {
		store.set(cfg)
	}
	tool.SetUserResolver(UserFromHeaders)
	return r.handler()
}

// Manifest returns the tool half of mod's manifest (routes are registered to
// discover the webhooks).
func Manifest(mod tool.Module, opts ...Option) wickplugin.ToolModule {
	_, _, tm := build(mod, opts)
	return tm
}

// ServeTool is the entire main() of a tool plugin binary. With
// --dump-manifest it prints the kind=tool manifest and exits; otherwise it
// serves HTTP on $WICK_PLUGIN_SOCKET plus the Tool control service until the
// host kills it.
func ServeTool(mod tool.Module, opts ...Option) {
	r, store, tm := build(mod, opts)
	dump, signKey := parseArgs(os.Args[1:])
	if dump {
		m, err := wickplugin.BuildSelfToolManifest(tm, signKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		b, _ := json.Marshal(m)
		fmt.Println(string(b))
		return
	}
	sock := os.Getenv(wickplugin.EnvToolSocket)
	if sock == "" {
		fmt.Fprintln(os.Stderr, "tool plugin: "+wickplugin.EnvToolSocket+" not set (run under wick)")
		os.Exit(1)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tool plugin: listen:", err)
		os.Exit(1)
	}
	_ = os.Chmod(sock, 0o600)
	wickplugin.ApplyRlimits()
	tool.SetUserResolver(UserFromHeaders)
	srv := &http.Server{Handler: r.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	schema, _ := json.Marshal(tm)
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: wickplugin.Handshake,
		VersionedPlugins: map[int]goplugin.PluginSet{
			wickplugin.ProtoVersion: {wickplugin.ToolPluginName: &wickplugin.ToolGRPCPlugin{Impl: &toolServer{schema: schema, cfg: store}}},
		},
		GRPCServer: wickplugin.GRPCServer,
	})
	_ = os.Remove(sock)
}

// parseArgs reads --dump-manifest and --sign-key[=]<path>.
func parseArgs(args []string) (dump bool, signKey string) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--dump-manifest":
			dump = true
		case a == "--sign-key" && i+1 < len(args):
			signKey = args[i+1]
			i++
		case strings.HasPrefix(a, "--sign-key="):
			signKey = strings.TrimPrefix(a, "--sign-key=")
		}
	}
	return dump, signKey
}
