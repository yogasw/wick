package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	wickenv "github.com/yogasw/wick/internal/pkg/env"
	"github.com/yogasw/wick/internal/pkg/netboot"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
)

// JobPluginName is the dispense key for job plugins.
const JobPluginName = "job"

// JobModule is the job half of a kind=job manifest: the job's static meta and
// its config fields (the RunFunc stays in the binary).
type JobModule struct {
	Meta    job.Meta        `json:"meta"`
	Configs []entity.Config `json:"configs,omitempty"`
}

// JobGRPCPlugin is the go-plugin descriptor for the Job service. Impl is set
// only on the plugin side.
type JobGRPCPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl pb.JobServer
}

func (p *JobGRPCPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterJobServer(s, p.Impl)
	return nil
}

func (p *JobGRPCPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &jobClient{inner: pb.NewJobClient(c)}, nil
}

// JobVersionedPlugins is the host-side plugin set for job plugins.
var JobVersionedPlugins = func() map[int]goplugin.PluginSet {
	out := map[int]goplugin.PluginSet{}
	for _, v := range SupportedProtoVersions() {
		out[v] = goplugin.PluginSet{JobPluginName: &JobGRPCPlugin{}}
	}
	return out
}()

// ── plugin side ────────────────────────────────────────────────────────

type mapCfg map[string]string

func (m mapCfg) GetOwned(_ string, key string) string { return m[key] }

type jobServer struct {
	pb.UnimplementedJobServer
	mod    job.Module
	schema []byte
}

// NewJobServer builds the plugin-side Job service for one job module.
func NewJobServer(mod job.Module) pb.JobServer {
	b, _ := json.Marshal(JobModule{Meta: mod.Meta, Configs: mod.Configs})
	return &jobServer{mod: mod, schema: b}
}

func (s *jobServer) Schema(context.Context, *pb.SchemaRequest) (*pb.SchemaResponse, error) {
	return &pb.SchemaResponse{ManifestJson: s.schema}, nil
}

func (s *jobServer) Health(context.Context, *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Healthy: true}, nil
}

func (s *jobServer) Run(req *pb.ExecuteRequest, stream grpc.ServerStreamingServer[pb.Chunk]) error {
	if s.mod.Run == nil {
		return stream.Send(&pb.Chunk{Eof: true, Error: &pb.Error{Code: "no_handler", Message: "job has no Run"}})
	}
	var mu sync.Mutex // Logf may be called from goroutines the job spawns
	ctx := job.WithCtx(stream.Context(), job.NewCtx(s.mod.Meta.Key, mapCfg(req.Creds)))
	ctx = job.WithLogger(ctx, func(line string) {
		mu.Lock()
		defer mu.Unlock()
		_ = stream.Send(&pb.Chunk{Message: line})
	})
	result, err := s.mod.Run(ctx)
	mu.Lock()
	defer mu.Unlock()
	if err != nil {
		return stream.Send(&pb.Chunk{Eof: true, Data: []byte(result), Error: &pb.Error{Code: "run_failed", Message: err.Error()}})
	}
	return stream.Send(&pb.Chunk{Eof: true, Data: []byte(result)})
}

// BuildSelfJobManifest is BuildSelfManifest for a job binary: kind=job, the
// job meta in Job, and Module.Meta mirroring key/name/description/icon so the
// shared install/scan code (keyed by Module.Meta.Key) works unchanged.
func BuildSelfJobManifest(mod job.Module, signKeyPath string) (Manifest, error) {
	m, err := BuildSelfManifest(connector.Module{Meta: connector.Meta{
		Key:         mod.Meta.Key,
		Name:        mod.Meta.Name,
		Description: mod.Meta.Description,
		Icon:        mod.Meta.Icon,
	}}, signKeyPath)
	if err != nil {
		return Manifest{}, err
	}
	m.Kind = KindJob
	m.Job = &JobModule{Meta: mod.Meta, Configs: mod.Configs}
	return m, nil
}

// ServeJob is the entire main() of a job plugin binary. With --dump-manifest it
// prints the kind=job manifest and exits; otherwise it serves the Job service
// until the host kills it (right after Run returns).
func ServeJob(mod job.Module) {
	if wickenv.IsTermux() {
		netboot.Setup()
	}
	dump, signKey := parseServeArgs(os.Args[1:])
	if dump {
		m, err := BuildSelfJobManifest(mod, signKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		b, err := json.Marshal(m)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(string(b))
		return
	}
	applyRlimits()
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		VersionedPlugins: map[int]goplugin.PluginSet{
			ProtoVersion: {JobPluginName: &JobGRPCPlugin{Impl: NewJobServer(mod)}},
		},
		GRPCServer: grpcServerWithLimits,
	})
}

// parseServeArgs reads --dump-manifest and --sign-key[=]<path>.
func parseServeArgs(args []string) (dump bool, signKey string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dump-manifest":
			dump = true
		case "--sign-key":
			if i+1 < len(args) {
				signKey = args[i+1]
				i++
			}
		default:
			if v, ok := strings.CutPrefix(args[i], "--sign-key="); ok {
				signKey = v
			}
		}
	}
	return dump, signKey
}

// ── host side ──────────────────────────────────────────────────────────

// ErrJobRun wraps a run failure reported by the job itself (vs a transport
// error). Callers can errors.Is against it.
var ErrJobRun = errors.New("job run failed")

// JobConn is the host-facing surface of a job plugin client.
type JobConn interface {
	// Run calls the job once. onLog receives each progress line as it
	// streams; the returned string is the job's markdown result.
	Run(ctx context.Context, trigger string, cfg map[string]string, onLog func(string)) (string, error)
	Schema(ctx context.Context) ([]byte, error)
}

type jobClient struct{ inner pb.JobClient }

func (c *jobClient) Schema(ctx context.Context) ([]byte, error) {
	resp, err := c.inner.Schema(ctx, &pb.SchemaRequest{})
	if err != nil {
		return nil, err
	}
	return resp.ManifestJson, nil
}

func (c *jobClient) Run(ctx context.Context, trigger string, cfg map[string]string, onLog func(string)) (string, error) {
	stream, err := c.inner.Run(ctx, &pb.ExecuteRequest{Operation: trigger, Creds: cfg})
	if err != nil {
		return "", err
	}
	for {
		ch, err := stream.Recv()
		if err == io.EOF {
			return "", fmt.Errorf("job stream ended without a result")
		}
		if err != nil {
			return "", err
		}
		if ch.Message != "" && onLog != nil {
			onLog(ch.Message)
		}
		if !ch.Eof {
			continue
		}
		if ch.Error != nil {
			return string(ch.Data), fmt.Errorf("%w: %s", ErrJobRun, ch.Error.Message)
		}
		return string(ch.Data), nil
	}
}
