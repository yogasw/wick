package connector

import (
	"context"
	"testing"
)

func TestCtxConfigsAndInputsReturnCopies(t *testing.T) {
	c := NewPluginCtx(context.Background(),
		map[string]string{"token": "secret"},
		map[string]string{"text": "hi"})

	cfg := c.Configs()
	in := c.Inputs()
	if cfg["token"] != "secret" || in["text"] != "hi" {
		t.Fatalf("accessors returned wrong data: %v %v", cfg, in)
	}
	cfg["token"] = "tampered"
	if c.Cfg("token") != "secret" {
		t.Fatal("Configs() returned a live reference, not a copy")
	}
}
