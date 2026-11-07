package main

import (
	"context"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestOwnsSession(t *testing.T) {
	a := connector.NewCtx(context.Background(), "aaaaaa11-2222", nil, nil, nil, nil, nil)
	b := connector.NewCtx(context.Background(), "bbbbbb33-4444", nil, nil, nil, nil, nil)
	id := newSessionID(a)
	if !ownsSession(a, id) {
		t.Fatalf("instance a should own %s", id)
	}
	if ownsSession(b, id) {
		t.Fatalf("instance b should not own %s", id)
	}
	anon := connector.NewCtx(context.Background(), "", nil, nil, nil, nil, nil)
	if !ownsSession(anon, newSessionID(anon)) || ownsSession(anon, id) {
		t.Fatal("an instance without id keeps only its own pw- sessions")
	}
}
