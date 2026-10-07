package channels

import (
	"context"
	"os"
	"strings"
	"testing"
)

type sendFnChannel struct {
	recordingChannel
	fn SendFunc
}

func (c *sendFnChannel) SetSendFunc(fn SendFunc) { c.fn = fn }

// A channel added after boot (a Team agent's bot, a per-user instance)
// takes its dispatch from the registry, so the registry must hold one.
func TestRuntimeChannelGetsBootSendFunc(t *testing.T) {
	called := false
	reg := NewRegistry().WithSendFunc(func(context.Context, string, string, string, string, string) error {
		called = true
		return nil
	})
	fn := reg.SendFuncFor("slack")
	if fn == nil {
		t.Fatal("SendFuncFor returned nil after WithSendFunc")
	}
	ch := &sendFnChannel{}
	reg.AddKeyed("slack:agent-x", ch, nil)
	if ch.fn == nil {
		t.Fatal("AddKeyed did not hand the channel the registry's sendFn")
	}
	_ = ch.fn(context.Background(), "s", "main", "slack", "user", "hi")
	if !called {
		t.Fatal("channel's sendFn is not the registry's")
	}
}

// Boot must wire the registry's sendFn before any channel can be added,
// or every runtime-added channel gets nil and panics on its first message.
func TestBootWiresSendFuncBeforeChannels(t *testing.T) {
	b, err := os.ReadFile("../../pkg/api/server.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	wire := strings.Index(src, "channelReg.WithSendFunc(")
	setup := strings.Index(src, "channelsetup.All(channelReg")
	runtime := strings.Index(src, "agentstool.RegisterAgentSlackInstances(")
	if wire < 0 || setup < 0 || runtime < 0 {
		t.Fatalf("anchors missing: wire=%d setup=%d runtime=%d", wire, setup, runtime)
	}
	if wire > setup || wire > runtime {
		t.Fatal("channelReg.WithSendFunc must run before channels are registered")
	}
}
