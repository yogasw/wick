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

// UserName exists so a connector can hand the MODEL a name instead of a uuid.
// Without a resolver wired (internal callers, tests) it reports nothing rather
// than inventing one, and the caller decides what to say instead.
func TestCtxUserName(t *testing.T) {
	var bare Ctx
	if got := bare.UserName("user-ada"); got != "" {
		t.Errorf("no resolver: UserName = %q, want empty", got)
	}

	var c Ctx
	c.SetUserNameResolver(func(id string) (string, bool) {
		if id == "user-ada" {
			return "Ada Lovelace", true
		}
		return "", false
	})

	if got := c.UserName("user-ada"); got != "Ada Lovelace" {
		t.Errorf("UserName = %q, want the resolved name", got)
	}
	// A miss and an empty id both report nothing — the resolver is never
	// asked about "" , and a user it cannot find is not named.
	if got := c.UserName("user-gone"); got != "" {
		t.Errorf("resolver miss: UserName = %q, want empty", got)
	}
	if got := c.UserName(""); got != "" {
		t.Errorf("empty id: UserName = %q, want empty", got)
	}
}
