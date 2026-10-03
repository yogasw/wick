package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	goplugin "github.com/hashicorp/go-plugin"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
)

// Manifest returns the service half of mod's manifest.
func Manifest(mod Module) wickplugin.ServiceModule {
	sm := wickplugin.ServiceModule{
		Meta: wickplugin.ToolMeta{
			Key: mod.Meta.Key, Name: mod.Meta.Name, Description: mod.Meta.Description, Icon: mod.Meta.Icon,
		},
		Routes:         mod.Routes,
		Configs:        mod.Configs,
		CallbackScopes: mod.CallbackScopes,
	}
	if mod.RemoteSource != nil {
		sm.Capabilities = append(sm.Capabilities, wickplugin.CapRemoteSource)
	}
	return sm
}

// Handler returns the plugin's HTTP handler without serving it — for tests
// and local harnesses. cfg seeds the config.
func Handler(mod Module, cfg map[string]string) http.Handler {
	h, env := build(mod)
	if cfg != nil {
		env.setCfg(cfg)
	}
	return h
}

func build(mod Module) (http.Handler, *Env) {
	env := newEnv(mod.Meta.Key)
	mux := http.NewServeMux()
	if mod.Register != nil {
		mod.Register(mux, env)
	}
	if mod.RemoteSource != nil {
		mountRemote(mux, mod.RemoteSource)
	}
	return mux, env
}

// mountRemote serves the remote_source RPC on wickplugin.RemotePath*.
func mountRemote(mux *http.ServeMux, src RemoteSource) {
	mux.HandleFunc("POST "+wickplugin.RemotePathSend, func(w http.ResponseWriter, r *http.Request) {
		var turn RemoteTurn
		if err := json.NewDecoder(r.Body).Decode(&turn); err != nil {
			http.Error(w, "bad turn: "+err.Error(), http.StatusBadRequest)
			return
		}
		res, err := src.Send(r.Context(), turn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("GET "+wickplugin.RemotePathEvents, func(w http.ResponseWriter, r *http.Request) {
		ch, err := src.Receive(r.Context(), r.URL.Query().Get("handle"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, _ := w.(http.Flusher)
		for ev := range ch {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if fl != nil {
				fl.Flush()
			}
		}
	})
	mux.HandleFunc("POST "+wickplugin.RemotePathDone, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Handle string `json:"handle"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		src.Done(body.Handle)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET "+wickplugin.RemotePathDescribe, func(w http.ResponseWriter, _ *http.Request) {
		out := map[string]string{}
		if d, ok := src.(Describer); ok {
			out["name"], out["detail"] = d.Describe()
		}
		writeJSON(w, out)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type ctlServer struct {
	pb.UnimplementedToolServer
	schema []byte
	env    *Env
}

func (s *ctlServer) Schema(context.Context, *pb.SchemaRequest) (*pb.SchemaResponse, error) {
	return &pb.SchemaResponse{ManifestJson: s.schema}, nil
}

func (s *ctlServer) Configure(_ context.Context, req *pb.ExecuteRequest) (*pb.HealthResponse, error) {
	s.env.setCfg(req.Creds)
	return &pb.HealthResponse{Healthy: true}, nil
}

func (s *ctlServer) Health(context.Context, *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Healthy: true}, nil
}

// ServeService is the entire main() of a service plugin binary. With
// --dump-manifest it prints the kind=service manifest and exits; otherwise
// it serves HTTP on $WICK_PLUGIN_SOCKET plus the control service until the
// host stops it.
func ServeService(mod Module) {
	sm := Manifest(mod)
	dump, signKey := parseArgs(os.Args[1:])
	if dump {
		m, err := wickplugin.BuildSelfServiceManifest(sm, signKey)
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
		fmt.Fprintln(os.Stderr, "service plugin: "+wickplugin.EnvToolSocket+" not set (run under wick)")
		os.Exit(1)
	}
	h, env := build(mod)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "service plugin: listen:", err)
		os.Exit(1)
	}
	_ = os.Chmod(sock, 0o600)
	wickplugin.ApplyRlimits()
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	schema, _ := json.Marshal(sm)
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: wickplugin.Handshake,
		VersionedPlugins: map[int]goplugin.PluginSet{
			wickplugin.ProtoVersion: {wickplugin.ToolPluginName: &wickplugin.ToolGRPCPlugin{Impl: &ctlServer{schema: schema, env: env}}},
		},
		GRPCServer: wickplugin.GRPCServer,
	})
	_ = os.Remove(sock)
}

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
