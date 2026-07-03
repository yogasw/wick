package router9

import (
	"strings"
	"testing"
	"time"
)

func TestBroadcasterNoSubscribersSkipsCapture(t *testing.T) {
	b := newBroadcaster()
	if b.hasSubscribers() {
		t.Fatal("fresh broadcaster should have no subscribers")
	}
	// publish with no subscribers must not block or panic.
	b.publish(ReqEvent{Method: "POST"})
}

func TestBroadcasterDeliversToSubscriber(t *testing.T) {
	b := newBroadcaster()
	ch, unsub := b.subscribe()
	if !b.hasSubscribers() {
		t.Fatal("subscribe did not register a subscriber")
	}
	b.publish(ReqEvent{Method: "POST", Path: "/v1/messages"})
	select {
	case e := <-ch:
		if e.Path != "/v1/messages" {
			t.Errorf("got path %q", e.Path)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
	unsub()
	if b.hasSubscribers() {
		t.Error("unsubscribe did not clear the subscriber")
	}
}

func TestBroadcasterFanOut(t *testing.T) {
	b := newBroadcaster()
	ch1, u1 := b.subscribe()
	ch2, u2 := b.subscribe()
	defer u1()
	defer u2()
	b.publish(ReqEvent{Model: "cc/opus"})
	for i, ch := range []<-chan ReqEvent{ch1, ch2} {
		select {
		case e := <-ch:
			if e.Model != "cc/opus" {
				t.Errorf("sub %d got model %q", i, e.Model)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d did not receive event", i)
		}
	}
}

func TestBroadcasterSlowSubscriberDoesNotBlock(t *testing.T) {
	b := newBroadcaster()
	_, unsub := b.subscribe() // never drained
	defer unsub()
	// Publish more than the buffer; extra events are dropped for the slow
	// subscriber but publish must always return promptly.
	done := make(chan struct{})
	go func() {
		for i := 0; i < subChanBuffer*3; i++ {
			b.publish(ReqEvent{Method: "POST"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
}

func TestRedactAuth(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"Bearer sk_9router_abcdef": "sk_9r…",
		"sk_9router_abcdef":        "sk_9r…",
		"x":                        "x…",
		"abcd":                     "abcd…",
	}
	for in, want := range cases {
		if got := redactAuth(in); got != want {
			t.Errorf("redactAuth(%q) = %q, want %q", in, got, want)
		}
	}
	if got := redactAuth("Bearer supersecretlongtoken12345"); strings.Contains(got, "supersecret") {
		t.Errorf("redactAuth leaked secret: %q", got)
	}
}

func TestSniffModel(t *testing.T) {
	cases := map[string]string{
		`{"model":"cc/opus","messages":[]}`: "cc/opus",
		`{"messages":[],"model": "gpt-5"}`:  "gpt-5",
		`{"model" :  "spaced"}`:             "spaced",
		`{"no_model":true}`:                 "",
		`not json at all`:                   "",
		`{"model":123}`:                     "",
	}
	for in, want := range cases {
		if got := sniffModel([]byte(in)); got != want {
			t.Errorf("sniffModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	local := []string{"127.0.0.1", "127.0.0.1:9425", "localhost", "localhost:80", "::1", "[::1]:443", "[::ffff:127.0.0.1]:80"}
	remote := []string{"10.0.0.5", "10.0.0.5:9425", "local2.yogasw.my.id", "local2.yogasw.my.id:443", "8.8.8.8", ""}
	for _, h := range local {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false, want true", h)
		}
	}
	for _, h := range remote {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, want false", h)
		}
	}
}
