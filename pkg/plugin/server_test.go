package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	pb "github.com/yogasw/wick/pkg/plugin/proto"
	"github.com/yogasw/wick/pkg/wickdocs"
)

func sampleModule() connector.Module {
	echo := func(c *connector.Ctx) (any, error) {
		return map[string]string{"said": c.Input("text")}, nil
	}
	return connector.Module{
		Meta:    connector.Meta{Key: "sample", Name: "Sample"},
		Configs: entity.StructToConfigs(struct{}{}),
		Operations: []connector.Category{
			connector.Cat("Main", "",
				connector.Op("say", "Say", "echoes text",
					struct {
						Text string `wick:"text"`
					}{}, echo, wickdocs.Docs{})),
		},
	}
}

func TestServerSchemaIsModuleJSON(t *testing.T) {
	srv := NewServer(sampleModule())
	resp, err := srv.Schema(context.Background(), &pb.SchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(sampleModule())
	if string(resp.ManifestJson) != string(want) {
		t.Fatalf("manifest drift:\n got %s\nwant %s", resp.ManifestJson, want)
	}
}

func TestServerExecuteRunsOp(t *testing.T) {
	srv := NewServer(sampleModule())
	args, _ := json.Marshal(map[string]string{"text": "hi"})
	resp, err := srv.Execute(context.Background(), &pb.ExecuteRequest{Operation: "say", ArgsJson: args})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected op error: %v", resp.Error)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.ResultJson, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if out["said"] != "hi" {
		t.Fatalf("got %v", out)
	}
}

func TestServerExecuteUnknownOp(t *testing.T) {
	srv := NewServer(sampleModule())
	resp, _ := srv.Execute(context.Background(), &pb.ExecuteRequest{Operation: "nope"})
	if resp.Error == nil {
		t.Fatal("expected error for unknown op")
	}
}

func TestServerResolveIdentity(t *testing.T) {
	mod := sampleModule()
	mod.OAuth = &connector.OAuthMeta{
		GetUserIdentity: func(_ context.Context, token string) (string, string, error) {
			return "U" + token, "User " + token, nil
		},
	}
	srv := NewServer(mod)
	resp, err := srv.ResolveIdentity(context.Background(), &pb.IdentityRequest{AccessToken: "123"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.UserId != "U123" || resp.DisplayName != "User 123" {
		t.Fatalf("identity wrong: %+v", resp)
	}
}

func TestServerResolveIdentityNoOAuth(t *testing.T) {
	srv := NewServer(sampleModule())
	resp, _ := srv.ResolveIdentity(context.Background(), &pb.IdentityRequest{AccessToken: "x"})
	if resp.Error == nil {
		t.Fatal("expected error when connector has no OAuth")
	}
}

// fakeChunkStream implements grpc.ServerStreamingServer[pb.Chunk] for tests.
// Only Send and Context are exercised by the handler.
type fakeChunkStream struct {
	grpc.ServerStream
	ctx    context.Context
	chunks []*pb.Chunk
}

func (f *fakeChunkStream) Send(c *pb.Chunk) error { f.chunks = append(f.chunks, c); return nil }
func (f *fakeChunkStream) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}

func streamModule(result string) connector.Module {
	return connector.Module{
		Meta: connector.Meta{Key: "demo", Name: "Demo"},
		Operations: []connector.Category{{Title: "Main", Ops: []connector.Operation{{
			Key: "say", Name: "Say",
			Execute: func(_ *connector.Ctx) (any, error) { return map[string]string{"v": result}, nil },
		}}}},
	}
}

func TestExecuteStreamChunksAndReassembles(t *testing.T) {
	big := strings.Repeat("x", (1<<20)*2+123) // >2 MiB to force multiple chunks
	srv := NewServer(streamModule(big)).(*grpcServer)
	st := &fakeChunkStream{}
	if err := srv.ExecuteStream(&pb.ExecuteRequest{Operation: "say"}, st); err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	if len(st.chunks) < 3 {
		t.Fatalf("expected multiple data chunks + EOF, got %d", len(st.chunks))
	}
	last := st.chunks[len(st.chunks)-1]
	if !last.Eof {
		t.Fatal("final chunk must have Eof=true")
	}
	var buf bytes.Buffer
	for _, c := range st.chunks {
		buf.Write(c.Data)
	}
	if !strings.Contains(buf.String(), big) {
		t.Fatal("reassembled payload missing the big result")
	}
}

func TestExecuteStreamSurfacesOpError(t *testing.T) {
	srv := NewServer(streamModule("")).(*grpcServer)
	st := &fakeChunkStream{}
	if err := srv.ExecuteStream(&pb.ExecuteRequest{Operation: "nope"}, st); err != nil {
		t.Fatalf("handler should report op errors via Chunk.Error, not return err: %v", err)
	}
	if len(st.chunks) != 1 || st.chunks[0].Error == nil {
		t.Fatalf("unknown op should yield one Chunk{Error}, got %+v", st.chunks)
	}
}

func maskModule() connector.Module {
	return connector.Module{
		Meta: connector.Meta{Key: "demo", Name: "Demo"},
		Operations: []connector.Category{{Title: "Main", Ops: []connector.Operation{{
			Key: "say", Name: "Say",
			Execute: func(c *connector.Ctx) (any, error) {
				v := c.Mask("top-secret", []string{"top-secret"})
				w := c.MaskIgnoreCase("Keyword", []string{"keyword"})
				return map[string]string{"v": v, "w": w}, nil
			},
		}}}},
	}
}

// The plugin holds no encryption key: c.Mask must leave the data as it is and
// report the values so the host can mask them before the result leaves wick.
func TestExecuteReportsMaskValues(t *testing.T) {
	srv := NewServer(maskModule())
	resp, err := srv.Execute(context.Background(), &pb.ExecuteRequest{Operation: "say"})
	if err != nil || resp.Error != nil {
		t.Fatalf("execute: %v %v", err, resp.Error)
	}
	if string(resp.ResultJson) != `{"v":"top-secret","w":"Keyword"}` {
		t.Fatalf("result changed in the plugin: %s", resp.ResultJson)
	}
	if len(resp.Mask) != 1 || resp.Mask[0] != "top-secret" {
		t.Fatalf("mask = %v", resp.Mask)
	}
	if len(resp.MaskIgnoreCase) != 1 || resp.MaskIgnoreCase[0] != "keyword" {
		t.Fatalf("mask_ignore_case = %v", resp.MaskIgnoreCase)
	}
}

func TestExecuteStreamReportsMaskValuesOnEof(t *testing.T) {
	srv := NewServer(maskModule()).(*grpcServer)
	st := &fakeChunkStream{}
	if err := srv.ExecuteStream(&pb.ExecuteRequest{Operation: "say"}, st); err != nil {
		t.Fatal(err)
	}
	last := st.chunks[len(st.chunks)-1]
	if !last.Eof || len(last.Mask) != 1 || len(last.MaskIgnoreCase) != 1 {
		t.Fatalf("Eof chunk must carry the mask values: %+v", last)
	}
}
