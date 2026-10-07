package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEncodeDecodePathRoundTrip(t *testing.T) {
	cases := [][]string{
		{"codex"},
		{"codex", "akun A"},
		{"openai-codex", "a@b.c", "x/y"},
		{"100%"},
	}
	for _, path := range cases {
		enc := EncodePath(path)
		if got := DecodePath(enc); !reflect.DeepEqual(got, path) {
			t.Fatalf("round trip %q → %q → %q", path, enc, got)
		}
		pin := EncodePin(path, "openai/gpt-5@x")
		p, m, ok := DecodePin(pin)
		if !ok || !reflect.DeepEqual(p, path) || m != "openai/gpt-5@x" {
			t.Fatalf("pin %q decoded to %q %q %v", pin, p, m, ok)
		}
	}
}

// wick's historical pins stay valid: "<entry>@<vendor model>" is a
// one-segment path, and the vendor model may carry '/' or '@'.
func TestDecodePinLegacyWick(t *testing.T) {
	p, m, ok := DecodePin("m_0370951f-68d@models/gemini-2.5-pro")
	if !ok || !reflect.DeepEqual(p, []string{"m_0370951f-68d"}) || m != "models/gemini-2.5-pro" {
		t.Fatalf("legacy pin: %q %q %v", p, m, ok)
	}
	if _, m, ok := DecodePin("opus"); ok || m != "opus" {
		t.Fatalf("flat model must not decode as grouped: %q %v", m, ok)
	}
}

type fakeSets struct{ err error }

func (fakeSets) Sets(context.Context, Instance) ([]ModelChoice, error) { return nil, nil }
func (fakeSets) Expand(context.Context, Instance, []string) ([]ModelChoice, error) {
	return nil, nil
}
func (f fakeSets) Resolve(_ Instance, path []string, model string) (SpawnPin, error) {
	if f.err != nil {
		return SpawnPin{}, f.err
	}
	sp := SpawnPin{Model: model, Provider: path[0]}
	if len(path) > 1 {
		sp.Account = path[1]
	}
	return sp, nil
}

func TestModelArgsResolvesGroupedPinViaRegistry(t *testing.T) {
	const typ = Type("fake-grouped")
	RegisterModelSets(typ, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(typ, nil) })
	ins := &Instance{Type: typ, Name: "x"}
	got := ModelArgs(SpawnOptions{Instance: ins, ModelID: EncodePin([]string{"openai-codex", "2"}, "openai-codex/gpt-5.5")}, nil)
	if !reflect.DeepEqual(got, []string{"--model", "openai-codex/gpt-5.5"}) {
		t.Fatalf("got %q", got)
	}
	// A refusing Resolve falls back to the old rule (foreign pin → no flag).
	RegisterModelSets(typ, fakeSets{err: errors.New("no")})
	if got := ModelArgs(SpawnOptions{Instance: ins, ModelID: "a@b"}, nil); got != nil {
		t.Fatalf("refused pin must drop the flag, got %q", got)
	}
	// Unregistered types keep the old behaviour untouched.
	claude := &Instance{Type: TypeClaude, Name: "c"}
	if got := ModelArgs(SpawnOptions{Instance: claude, ModelID: "m_1@x"}, nil); got != nil {
		t.Fatalf("claude wick pin must drop, got %q", got)
	}
}

func withModelStateDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// Quota exhaustion is process-global too: a repeated run (-count=N)
	// must not inherit the previous run's exhausted accounts.
	exhaustedMu.Lock()
	exhaustedAt = map[string]time.Time{}
	exhaustedMu.Unlock()
	prev := modelStateDir
	modelStateDir = func() (string, error) { return dir, nil }
	modelStateMu.Lock()
	modelStateCache = map[string]*modelStateFile{}
	modelStateMu.Unlock()
	t.Cleanup(func() {
		modelStateDir = prev
		modelStateMu.Lock()
		modelStateCache = map[string]*modelStateFile{}
		modelStateMu.Unlock()
	})
}

func TestModelAvailabilityDefaultAndGreyOut(t *testing.T) {
	withModelStateDir(t)
	ins := Instance{Type: TypeOMP, Name: "yoga"}
	rows := []ModelChoice{{ID: "gpt-5.5", Default: true}, {ID: "gpt-5.4"}, {ID: "gpt-5.4-mini"}}

	// Nothing known: the incoming default stands.
	if got := ApplyAvailability(ins, "", rows); !got[0].Default {
		t.Fatalf("default lost: %+v", got)
	}
	MarkModelUnavailable(ins, "", "gpt-5.5", "plan free")
	got := ApplyAvailability(ins, "", rows)
	if !got[0].Unavailable || got[0].Default || !got[1].Default {
		t.Fatalf("refused model must grey out and hand default to next: %+v", got)
	}
	// Last worked beats list order.
	MarkModelWorked(ins, "", "gpt-5.4-mini")
	got = ApplyAvailability(ins, "", rows)
	if !got[2].Default || got[1].Default {
		t.Fatalf("last-worked must be default: %+v", got)
	}
	// Per account: account "2" knows nothing.
	if got := ApplyAvailability(ins, "2", rows); got[0].Unavailable {
		t.Fatalf("refusal leaked across accounts: %+v", got)
	}
	// Persisted across a cold cache.
	modelStateMu.Lock()
	modelStateCache = map[string]*modelStateFile{}
	modelStateMu.Unlock()
	if _, bad := ModelUnavailable(ins, "", "gpt-5.5"); !bad {
		t.Fatal("refusal not persisted")
	}
	if LastWorkedModel(ins, "") != "gpt-5.4-mini" {
		t.Fatal("last worked not persisted")
	}
	ResetModelAvailability(ins)
	if _, bad := ModelUnavailable(ins, "", "gpt-5.5"); bad {
		t.Fatal("refresh must reset refusals")
	}
	if LastWorkedModel(ins, "") != "gpt-5.4-mini" {
		t.Fatal("refresh must keep last worked")
	}
}

func TestIsModelAccessErrorAndMessage(t *testing.T) {
	for _, m := range []string{
		`{"error":{"code":"model_not_found"}}`,
		"You do not have access to the model gpt-5.5",
		"The 'gpt-5.5' model is not supported when using Codex with a ChatGPT account.",
	} {
		if !IsModelAccessError(m) {
			t.Fatalf("not detected: %q", m)
		}
	}
	if IsModelAccessError("429 rate limited") {
		t.Fatal("429 is not an access error")
	}
	if got := ModelUnavailableMessage("openai-codex/gpt-5.5", "free"); got != "gpt-5.5 tidak tersedia untuk akun free ini — pilih model lain." {
		t.Fatalf("msg %q", got)
	}
}

// ApplyAvailability runs on HTTP handlers while Mark* run on an agent's
// stdout reader; the picker must not read the shared map after the lock
// is released (go test -race).
func TestApplyAvailabilityConcurrentWithMark(t *testing.T) {
	withModelStateDir(t)
	ins := Instance{Type: TypeOMP, Name: "race"}
	rows := []ModelChoice{{ID: "m0"}, {ID: "m1"}, {ID: "m2"}}
	MarkModelUnavailable(ins, "", "m0", "seed")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			MarkModelUnavailable(ins, "", fmt.Sprintf("x%d", i), "r")
			MarkModelWorked(ins, "", fmt.Sprintf("x%d", i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			ApplyAvailability(ins, "", rows)
		}
	}()
	wg.Wait()
}

// flatSets is a two-provider picker: openai-codex has two accounts (Auto +
// #1 + #2), anthropic one.
type flatSets struct{}

func (flatSets) Sets(context.Context, Instance) ([]ModelChoice, error) {
	return []ModelChoice{{ID: "openai-codex", Live: true, Default: true}, {ID: "anthropic", Live: true}}, nil
}

func (flatSets) Expand(_ context.Context, _ Instance, path []string) ([]ModelChoice, error) {
	switch strings.Join(path, "/") {
	case "openai-codex":
		return []ModelChoice{{ID: AutoAccount, Live: true, Default: true}, {ID: "1", Live: true}, {ID: "2", Live: true}}, nil
	case "openai-codex/" + AutoAccount:
		return []ModelChoice{{ID: "openai-codex/gpt-5.5", Unavailable: true}, {ID: "openai-codex/gpt-5.6-luna", Default: true}}, nil
	case "openai-codex/1", "openai-codex/2":
		return []ModelChoice{{ID: "openai-codex/per-account-dup"}}, nil
	case "anthropic":
		return []ModelChoice{{ID: "anthropic/claude"}}, nil
	}
	return nil, nil
}

func (flatSets) Resolve(Instance, []string, string) (SpawnPin, error) { return SpawnPin{}, nil }

// The provider page's flat list walks the picker's own levels: accounts
// collapse onto Auto (each model once), marks and default kept.
func TestFlattenModelSets(t *testing.T) {
	rows, err := FlattenModelSets(context.Background(), flatSets{}, Instance{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.ID)
	}
	want := []string{"openai-codex/gpt-5.5", "openai-codex/gpt-5.6-luna", "anthropic/claude"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	if !rows[0].Unavailable || !rows[1].Default {
		t.Fatalf("marks lost: %+v", rows)
	}
}
