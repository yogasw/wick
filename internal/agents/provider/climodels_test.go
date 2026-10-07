package provider

import (
	"context"
	"slices"
	"testing"
)

func TestParseOMPModels(t *testing.T) {
	out := []byte(`{"models":[{"provider":"openai-codex","kind":"chat","id":"gpt-5.5","selector":"openai-codex/gpt-5.5","name":"GPT-5.5"},{"selector":"x/embed","kind":"embedding"}]}`)
	got, err := parseOMPModels(out)
	if err != nil || len(got) != 1 || got[0].ID != "openai-codex/gpt-5.5" || got[0].Desc != "GPT-5.5" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestParseOpencodeModels(t *testing.T) {
	got := parseOpencodeModels([]byte("openai/gpt-5.5\n\nModels cache refreshed\nopencode/big-pickle\n{\"x\":1}\n"))
	ids := []string{}
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(ids, []string{"openai/gpt-5.5", "opencode/big-pickle"}) {
		t.Fatal(ids)
	}
}

func TestListCLIModelsRunsUnderAccountStore(t *testing.T) {
	old := cliModelsRunner
	t.Cleanup(func() { cliModelsRunner = old })
	var gotArgs, gotEnv []string
	cliModelsRunner = func(_ context.Context, _ string, args, env []string) ([]byte, error) {
		gotArgs, gotEnv = args, env
		return []byte(`{"models":[]}`), nil
	}
	// /bin/sh stands in for the binary; only the resolution must succeed.
	ins := Instance{Type: TypeOMP, Name: "m", Binary: "/bin/sh", OMPConfig: &OMPConfig{Profile: "wick-m"}}
	if _, err := ListCLIModels(context.Background(), ins); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gotArgs, []string{"--profile", "wick-m", "models", "--json"}) || !slices.Contains(gotEnv, "OMP_PROFILE=wick-m") {
		t.Fatalf("args %q env %q", gotArgs, gotEnv)
	}
	if _, err := ListCLIModels(context.Background(), Instance{Type: TypeClaude}); err == nil {
		t.Fatal("claude must be rejected")
	}
}
