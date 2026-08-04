package provider

import (
	"reflect"
	"testing"
)

func TestModelArgsPassesACLIModelName(t *testing.T) {
	got := ModelArgs(SpawnOptions{ModelID: "gpt-5-codex"}, nil)
	if want := []string{"--model", "gpt-5-codex"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestModelArgsSkipsWhenNothingPinned(t *testing.T) {
	if got := ModelArgs(SpawnOptions{}, nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestModelArgsRespectsAManualModelFlag(t *testing.T) {
	got := ModelArgs(SpawnOptions{ModelID: "opus"}, []string{"--model", "sonnet"})
	if got != nil {
		t.Fatalf("got %v, want nil — an operator's explicit --model wins", got)
	}
}

func TestModelArgsSkipsForAnAIRouterInstance(t *testing.T) {
	got := ModelArgs(SpawnOptions{ModelID: "opus", Instance: &Instance{UseAIRouter: true}}, nil)
	if got != nil {
		t.Fatalf("got %v, want nil — the router sets the model via env", got)
	}
}

// The spawn failure this guards: a wick pin that outlived the instance it was
// chosen on (a session switched away from wick, or a child that inherited its
// parent's wick pin) became `--model m_0370951f-68d`, which the CLI rejects.
func TestModelArgsDropsAWickRegistryID(t *testing.T) {
	if got := ModelArgs(SpawnOptions{ModelID: "m_0370951f-68d"}, nil); got != nil {
		t.Fatalf("got %v, want nil — a wick registry id is not a CLI model name", got)
	}
}

func TestModelArgsDropsALiveSetLeaf(t *testing.T) {
	if got := ModelArgs(SpawnOptions{ModelID: "set1@grok-4.5"}, nil); got != nil {
		t.Fatalf("got %v, want nil — an '@' pin addresses a wick live set", got)
	}
}

// The guard must be narrow: a real CLI alias that merely starts with "m" is
// still a legal model name.
func TestModelArgsKeepsNamesThatOnlyLookSimilar(t *testing.T) {
	for _, name := range []string{"mistral-large", "m4-preview", "model-x"} {
		got := ModelArgs(SpawnOptions{ModelID: name}, nil)
		if want := []string{"--model", name}; !reflect.DeepEqual(got, want) {
			t.Errorf("ModelArgs(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestIsForeignModelPin(t *testing.T) {
	foreign := []string{"m_0370951f-68d", "set1@grok-4.5", "m_abc", "x@y"}
	for _, v := range foreign {
		if !isForeignModelPin(v) {
			t.Errorf("isForeignModelPin(%q) = false, want true", v)
		}
	}
	native := []string{"opus", "sonnet", "gpt-5-codex", "mistral-large", "gemini-3-pro", ""}
	for _, v := range native {
		if isForeignModelPin(v) {
			t.Errorf("isForeignModelPin(%q) = true, want false", v)
		}
	}
}
