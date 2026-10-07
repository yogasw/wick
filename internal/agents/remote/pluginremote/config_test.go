package pluginremote

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/remote"
	wickentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/service"
)

// turnSource records the turn it was sent.
type turnSource struct {
	botSource
	got chan service.RemoteTurn
}

func (b *turnSource) Send(ctx context.Context, t service.RemoteTurn) (service.RemoteSendResult, error) {
	b.got <- t
	return b.botSource.Send(ctx, t)
}

func TestSendCarriesAgentConfig(t *testing.T) {
	bot := &turnSource{botSource: botSource{done: make(chan string, 1)}, got: make(chan service.RemoteTurn, 1)}
	src := NewSource("echo", serveSocket(t, service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: bot}, nil)))
	src.AgentID, src.Config = "agent-1", map[string]string{"api_key": "k-a", "source": "sources/a"}
	if _, err := src.Send(context.Background(), remote.Turn{Text: "hi", SessionDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	got := <-bot.got
	if got.AgentID != "agent-1" || got.Config["api_key"] != "k-a" || got.Config["source"] != "sources/a" {
		t.Fatalf("turn = %+v", got)
	}
}

func TestManifestCarriesRemoteConfigs(t *testing.T) {
	type rc struct {
		APIKey string `wick:"secret;desc=Key."`
	}
	decl := wickentity.StructToConfigs(rc{})
	m := service.Manifest(service.Module{Meta: service.Meta{Key: "x"}, RemoteSource: &botSource{}, RemoteConfigs: decl})
	if len(m.RemoteConfigs) != 1 || !m.RemoteConfigs[0].IsSecret {
		t.Fatalf("remote configs = %+v", m.RemoteConfigs)
	}
}

// prefixCodec "encrypts" by prefixing, enough to see what is sealed.
type prefixCodec struct{}

func (prefixCodec) EncryptSecret(p string) (string, error) { return "sealed:" + p, nil }
func (prefixCodec) DecryptSecret(t string) (string, error) {
	return strings.TrimPrefix(t, "sealed:"), nil
}

func TestSealOpenValues(t *testing.T) {
	decl := []wickentity.Config{{Key: "api_key", IsSecret: true}, {Key: "source"}}
	sealed, err := SealValues(prefixCodec{}, decl, map[string]string{"api_key": "k", "source": "s"})
	if err != nil || sealed["api_key"] != "sealed:k" || sealed["source"] != "s" {
		t.Fatalf("sealed = %v %v", sealed, err)
	}
	plain, err := OpenValues(prefixCodec{}, sealed)
	if err != nil || plain["api_key"] != "k" || plain["source"] != "s" {
		t.Fatalf("plain = %v %v", plain, err)
	}
}
