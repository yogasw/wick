package main

import (
	"strings"
	"testing"
	"time"
)

func TestReadHookCommandHappyPath(t *testing.T) {
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	got, err := readHookCommand(in, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ls -la" {
		t.Fatalf("got %q", got)
	}
}

func TestReadHookCommandEmpty(t *testing.T) {
	in := strings.NewReader("")
	if _, err := readHookCommand(in, time.Second); err == nil {
		t.Fatal("empty stdin should error")
	}
}

func TestReadHookCommandMalformed(t *testing.T) {
	in := strings.NewReader("not json")
	if _, err := readHookCommand(in, time.Second); err == nil {
		t.Fatal("malformed json should error")
	}
}

func TestReadHookCommandMissingCommandField(t *testing.T) {
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`)
	if _, err := readHookCommand(in, time.Second); err == nil {
		t.Fatal("missing command field should error")
	}
}

// blockingReader never returns — used to drive the timeout path.
type blockingReader struct{ ch chan struct{} }

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.ch
	return 0, nil
}

func TestReadHookCommandTimeout(t *testing.T) {
	r := &blockingReader{ch: make(chan struct{})}
	defer close(r.ch)
	start := time.Now()
	_, err := readHookCommand(r, 50*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout message, got %q", err.Error())
	}
}
