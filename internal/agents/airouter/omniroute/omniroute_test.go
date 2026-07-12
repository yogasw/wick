package omniroute

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/airouter"
	"github.com/yogasw/wick/internal/agents/provider"
)

func TestRegisteredWithAirouter(t *testing.T) {
	rt, ok := airouter.Get("omniroute")
	if !ok {
		t.Fatal("omniroute not registered with airouter")
	}
	if rt.Desc.NpmPackage != "omniroute" {
		t.Fatalf("descriptor mismatch: %+v", rt.Desc)
	}
}

func TestLaunchUsesPortEnv(t *testing.T) {
	args, env := launch(25001)
	if len(args) != 0 {
		t.Fatalf("omniroute sets port via env, not args: %v", args)
	}
	if !strings.Contains(strings.Join(env, " "), "PORT=25001") {
		t.Fatalf("PORT env not set: %v", env)
	}
}

func TestContributeCodexUsesOmnirouteProvider(t *testing.T) {
	args, env, err := (hook{}).Contribute(provider.TypeCodex, provider.Instance{}, "http://x/airouter/omniroute/v1", "key123")
	if err != nil {
		t.Fatal(err)
	}
	ja := strings.Join(args, " ")
	if !strings.Contains(ja, "model_provider=omniroute") {
		t.Fatalf("codex args missing omniroute provider: %v", args)
	}
	if !strings.Contains(ja, "wire_api=responses") {
		t.Fatalf("omniroute should use the Responses wire api (codex dropped chat): %v", args)
	}
	if strings.Join(env, " ") != "OPENAI_API_KEY=key123" {
		t.Fatalf("codex env = %v", env)
	}
}

func TestNoDefaultKey(t *testing.T) {
	if (hook{}).DefaultKey() != "" {
		t.Fatal("omniroute has no bare default key")
	}
}
