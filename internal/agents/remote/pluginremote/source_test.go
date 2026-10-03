package pluginremote

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/pkg/service"
)

// botSource is a remote_source plugin: echo, split in two deltas.
type botSource struct{ done chan string }

func (b *botSource) Send(_ context.Context, t service.RemoteTurn) (service.RemoteSendResult, error) {
	ctxID := t.ContextID
	if ctxID == "" {
		ctxID = "conv-1"
	}
	return service.RemoteSendResult{Handle: "h-" + t.Text, ContextID: ctxID}, nil
}

func (b *botSource) Receive(_ context.Context, h string) (<-chan service.RemoteEvent, error) {
	ch := make(chan service.RemoteEvent, 4)
	ch <- service.RemoteEvent{Kind: "status", Status: remote.StatusWorking}
	ch <- service.RemoteEvent{Kind: "text_delta", Text: "echo: "}
	ch <- service.RemoteEvent{Kind: "text_delta", Text: h[2:]}
	ch <- service.RemoteEvent{Kind: "done"}
	close(ch)
	return ch, nil
}

func (b *botSource) Done(h string) { b.done <- h }

func (b *botSource) Describe() (string, string) { return "Echo bot", "echoes" }

// serveSocket serves the plugin handler on a unix socket like the host does.
func serveSocket(t *testing.T, h http.Handler) Transport {
	dir, err := os.MkdirTemp("", "prs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	rt := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	return func(string) (http.RoundTripper, error) { return rt, nil }
}

func TestSendReceiveDone(t *testing.T) {
	bot := &botSource{done: make(chan string, 1)}
	src := NewSource("echo", serveSocket(t, service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: bot}, nil)))
	ctx := context.Background()
	dir := t.TempDir()

	h, err := src.Send(ctx, remote.Turn{Text: "hi", SessionDir: dir, SessionID: "s1"})
	if err != nil || h.ID != "h-hi" {
		t.Fatalf("send = %+v %v", h, err)
	}
	if got := src.ResumeID(dir); got != "plugin:echo:conv-1" {
		t.Fatalf("resume id = %q", got)
	}
	ch, err := src.Receive(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var last remote.Event
	for ev := range ch {
		if ev.Kind == remote.EventTextDelta {
			text += ev.Text
		}
		last = ev
	}
	if text != "echo: hi" || last.Kind != remote.EventDone {
		t.Fatalf("text=%q last=%+v", text, last)
	}
	src.End(h)
	select {
	case got := <-bot.done:
		if got != "h-hi" {
			t.Fatalf("done handle = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Done never reached the plugin")
	}
	d, err := src.Describe(ctx)
	if err != nil || d.Name != "Echo bot" {
		t.Fatalf("describe = %+v %v", d, err)
	}
	if a, ok := remote.Lookup(AdapterKind); !ok || a.Label != "Plugin" {
		t.Fatalf("adapter not registered: %+v", a)
	}
}

// TestImplementsRemoteInterfaces pins the adapter to what remote.Spawner
// (the process behind every remote agent session, and so its A2A/REST
// connection) needs.
func TestImplementsRemoteInterfaces(t *testing.T) {
	bot := &botSource{done: make(chan string, 1)}
	src := NewSource("echo", serveSocket(t, service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: bot}, nil)))
	var _ remote.Source = src
	var _ remote.Pusher = src
	var _ remote.Resumer = src
	_ = remote.Spawner{Source: src}
}
