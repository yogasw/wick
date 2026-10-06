package pluginremote

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/remote"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
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

// injectBot is botSource whose remote also takes a message mid-turn.
type injectBot struct {
	botSource
	got chan string
}

func (b *injectBot) Inject(_ context.Context, h, text string) error {
	b.got <- h + "|" + text
	return nil
}

// countInject counts requests to the inject path in front of h.
func countInject(h http.Handler, n *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wickplugin.RemotePathInject {
			n.Add(1)
		}
		h.ServeHTTP(w, r)
	})
}

func TestInjectNeedsDeclaredSupport(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	plain := &botSource{done: make(chan string, 1)}
	src := NewSource("echo", serveSocket(t, countInject(service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: plain}, nil), &calls)))
	var _ remote.Injector = src
	err := src.Inject(ctx, remote.Handle{ID: "h-1"}, remote.Turn{Text: "more"})
	if !errors.Is(err, remote.ErrInjectUnsupported) {
		t.Fatalf("inject on a plugin without it = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("inject path called %d times for a plugin that does not declare it", calls.Load())
	}

	bot := &injectBot{botSource: botSource{done: make(chan string, 1)}, got: make(chan string, 1)}
	src = NewSource("echo", serveSocket(t, countInject(service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: bot}, nil), &calls)))
	if err := src.Inject(ctx, remote.Handle{ID: "h-1"}, remote.Turn{Text: "[from: Ana]\nngak bisa akses kah?"}); err != nil {
		t.Fatal(err)
	}
	if got := <-bot.got; got != "h-1|ngak bisa akses kah?" {
		t.Fatalf("plugin got %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("inject path called %d times", calls.Load())
	}
}

// cancelBot is botSource whose remote can also stop a running turn.
type cancelBot struct {
	botSource
	got chan string
}

func (b *cancelBot) Cancel(_ context.Context, h string) error {
	b.got <- h
	return nil
}

// countPath counts requests to path in front of h.
func countPath(h http.Handler, path string, n *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			n.Add(1)
		}
		h.ServeHTTP(w, r)
	})
}

func TestCancelNeedsDeclaredSupport(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	plain := &botSource{done: make(chan string, 1)}
	src := NewSource("echo", serveSocket(t, countPath(service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: plain}, nil), wickplugin.RemotePathCancel, &calls)))
	if err := src.Cancel(ctx, remote.Handle{ID: "h-1"}); err != nil {
		t.Fatalf("cancel on a plugin without it = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("cancel path called %d times for a plugin that does not declare it", calls.Load())
	}
	select {
	case h := <-plain.done:
		t.Fatalf("cancel released %q: Stop must not be Done", h)
	default:
	}

	bot := &cancelBot{botSource: botSource{done: make(chan string, 1)}, got: make(chan string, 1)}
	src = NewSource("echo", serveSocket(t, countPath(service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: bot}, nil), wickplugin.RemotePathCancel, &calls)))
	if err := src.Cancel(ctx, remote.Handle{ID: "h-1"}); err != nil {
		t.Fatal(err)
	}
	if got := <-bot.got; got != "h-1" {
		t.Fatalf("plugin cancelled %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("cancel path called %d times", calls.Load())
	}
}

// optsSource declares new-session fields and reports the values it used,
// the branch defaulted when the turn left it empty.
type optsSource struct {
	botSource
	got []map[string]string
}

func (o *optsSource) Send(ctx context.Context, t service.RemoteTurn) (service.RemoteSendResult, error) {
	o.got = append(o.got, t.Options)
	res, _ := o.botSource.Send(ctx, t)
	if t.ContextID == "" {
		used := map[string]string{"source": t.Options["source"], "branch": t.Options["branch"]}
		if used["branch"] == "" {
			used["branch"] = "main"
		}
		res.Options = used
	}
	return res, nil
}

func (o *optsSource) SessionFields(cfg map[string]string) []service.SessionField {
	return []service.SessionField{{Key: "source", Label: "Repository", Default: cfg["source"]}, {Key: "branch", Label: "Branch"}}
}

func TestSessionOptionsSentThenLocked(t *testing.T) {
	bot := &optsSource{botSource: botSource{done: make(chan string, 4)}}
	src := NewSource("echo", serveSocket(t, service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: bot}, nil)))
	src.Config = map[string]string{"source": "o/r"}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "s1") // not created yet, like a fresh chat

	fields, err := src.SessionFields(ctx)
	if err != nil || len(fields) != 2 || fields[0].Default != "o/r" {
		t.Fatalf("fields = %+v %v", fields, err)
	}
	if err := SaveSessionOptions(dir, map[string]string{"source": "me/app", "branch": " "}); err != nil {
		t.Fatal(err)
	}
	if opts, locked := SessionOptions(dir); locked || opts["source"] != "me/app" || opts["branch"] != "" {
		t.Fatalf("before send: %v locked=%v", opts, locked)
	}
	if _, err := src.Send(ctx, remote.Turn{Text: "hi", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	if bot.got[0]["source"] != "me/app" {
		t.Fatalf("first turn options = %v", bot.got[0])
	}
	opts, locked := SessionOptions(dir)
	if !locked || opts["source"] != "me/app" || opts["branch"] != "main" {
		t.Fatalf("after send: %v locked=%v", opts, locked)
	}
	if err := SaveSessionOptions(dir, map[string]string{"source": "x/y"}); !errors.Is(err, ErrSessionStarted) {
		t.Fatalf("save after start = %v, want ErrSessionStarted", err)
	}
	// A later turn keeps the same conversation and values.
	if _, err := src.Send(ctx, remote.Turn{Text: "again", SessionDir: dir}); err != nil {
		t.Fatal(err)
	}
	if opts, _ := SessionOptions(dir); opts["branch"] != "main" || src.ResumeID(dir) != "plugin:echo:conv-1" {
		t.Fatalf("after second send: %v", opts)
	}
}

func TestSessionFieldsNeedDeclaredSupport(t *testing.T) {
	bot := &botSource{done: make(chan string, 1)}
	src := NewSource("echo", serveSocket(t, service.Handler(service.Module{Meta: service.Meta{Key: "echo"}, RemoteSource: bot}, nil)))
	if f, err := src.SessionFields(context.Background()); err != nil || f != nil {
		t.Fatalf("fields = %v %v, want none", f, err)
	}
}
