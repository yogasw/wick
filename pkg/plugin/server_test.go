package plugin

import (
	"context"
	"encoding/json"
	"testing"

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
