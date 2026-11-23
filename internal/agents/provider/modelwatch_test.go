package provider

import "testing"

func TestModelTurnWatchMarksRefusedAndWorked(t *testing.T) {
	withModelStateDir(t)
	const typ = Type("fake-grouped")
	RegisterModelSets(typ, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(typ, nil) })
	// fakeSets is keyed by type; watch needs TypeOMP for turn-end markers.
	RegisterModelSets(TypeOMP, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOMP, nil) })
	ins := &Instance{Type: TypeOMP, Name: "yoga", LiveModels: true, ModelSelect: true}

	// No pin: the model is learned from the stream, the refusal recorded.
	w := newModelTurnWatch(ins, "")
	w.observe(`{"type":"message_start","message":{"provider":"openai-codex","model":"gpt-5.5"}}`)
	w.observe(`{"type":"message_end","message":{"stopReason":"error","errorMessage":"{\"error\":{\"code\":\"model_not_found\"}}","plan_type":"free"}}`)
	w.observe(`{"type":"agent_end"}`)
	if r, bad := ModelUnavailable(*ins, "openai-codex", "openai-codex/gpt-5.5"); !bad || r != "plan free" {
		t.Fatalf("refusal not recorded: %q %v", r, bad)
	}
	if LastWorkedModel(*ins, "") != "" {
		t.Fatal("a failed turn must not count as worked")
	}

	// A pinned account turn that ends clean becomes the default.
	w = newModelTurnWatch(ins, EncodePin([]string{"openai-codex", "2"}, "openai-codex/gpt-5.4"))
	w.observe(`{"type":"agent_end"}`)
	if LastWorkedModel(*ins, "openai-codex#2") != "openai-codex/gpt-5.4" || LastWorkedModel(*ins, "") != "openai-codex/gpt-5.4" {
		t.Fatal("worked turn not recorded")
	}

	// Default rule: refused first model skipped, last worked chosen.
	list := []ModelSeed{{ID: "openai-codex/gpt-5.5"}, {ID: "openai-codex/gpt-5.3"}, {ID: "openai-codex/gpt-5.4"}}
	if got := pickLiveDefault(*ins, list); got != "openai-codex/gpt-5.4" {
		t.Fatalf("default = %q", got)
	}
	// wick is never watched.
	if newModelTurnWatch(&Instance{Type: TypeWick}, "") != nil {
		t.Fatal("wick must not be watched")
	}
}

func TestModelTurnWatchErrorTurnIsNotWorked(t *testing.T) {
	withModelStateDir(t)
	RegisterModelSets(TypeOMP, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOMP, nil) })
	ins := &Instance{Type: TypeOMP, Name: "err-" + t.Name(), LiveModels: true, ModelSelect: true}

	// A retired id: the gateway 404s it. Refused, never "last worked".
	w := newModelTurnWatch(ins, "")
	w.observe(`{"type":"message_start","message":{"provider":"acme","model":"acme/old-free"}}`)
	w.observe(`{"type":"message_end","message":{"stopReason":"error","errorMessage":"404 The requested model 'acme/old-free' does not exist."}}`)
	w.observe(`{"type":"agent_end"}`)
	if _, bad := ModelUnavailable(*ins, "acme", "acme/old-free"); !bad {
		t.Fatal("a 404 unknown model must be recorded as refused")
	}
	if got := LastWorkedModel(*ins, ""); got != "" {
		t.Fatalf("a 404 turn counted as worked: %q", got)
	}

	// Any other request error (a quota hit) is not a refusal, but not a
	// worked turn either.
	w = newModelTurnWatch(ins, "")
	w.observe(`{"type":"message_start","message":{"provider":"acme","model":"acme/big"}}`)
	w.observe(`{"type":"message_end","message":{"stopReason":"error","errorMessage":"usage limit reached"}}`)
	w.observe(`{"type":"agent_end"}`)
	if _, bad := ModelUnavailable(*ins, "acme", "acme/big"); bad {
		t.Fatal("a quota hit is not a refusal")
	}
	if got := LastWorkedModel(*ins, ""); got != "" {
		t.Fatalf("an errored turn counted as worked: %q", got)
	}

	// The next clean turn still counts.
	w = newModelTurnWatch(ins, "")
	w.observe(`{"type":"message_start","message":{"provider":"acme","model":"acme/big"}}`)
	w.observe(`{"type":"agent_end"}`)
	if got := LastWorkedModel(*ins, ""); got != "acme/big" {
		t.Fatalf("clean turn not recorded: %q", got)
	}
}

func TestModelTurnWatchIgnoresQuotedAccessErrors(t *testing.T) {
	withModelStateDir(t)
	RegisterModelSets(TypeOMP, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOMP, nil) })
	ins := &Instance{Type: TypeOMP, Name: "quote-" + t.Name(), LiveModels: true, ModelSelect: true}

	// A turn whose tool output and answer quote access errors, but which
	// itself runs fine, must not refuse its model.
	w := newModelTurnWatch(ins, "")
	w.observe(`{"type":"message_start","message":{"provider":"acme","model":"acme/auto-free"}}`)
	w.observe(`{"type":"tool_execution_end","result":{"content":[{"type":"text","text":"log: {\"error\":{\"code\":\"model_not_found\"}} you do not have access"}]}}`)
	w.observe(`{"type":"message_end","message":{"stopReason":"stop","content":[{"type":"text","text":"The log says model_not_found."}]}}`)
	w.observe(`{"type":"agent_end"}`)
	if _, bad := ModelUnavailable(*ins, "acme", "acme/auto-free"); bad {
		t.Fatal("a quoted access error refused a working model")
	}
	if got := LastWorkedModel(*ins, ""); got != "acme/auto-free" {
		t.Fatalf("clean turn not recorded as worked: %q", got)
	}
}

func TestPickLiveDefaultWithoutEvidenceIsListOrder(t *testing.T) {
	withModelStateDir(t)
	ins := Instance{Type: TypeOpencode, Name: "x"}
	if got := pickLiveDefault(ins, []ModelSeed{{ID: "a/1"}, {ID: "a/2"}}); got != "a/1" {
		t.Fatalf("got %q", got)
	}
	MarkModelUnavailable(ins, "a", "a/1", "")
	if got := pickLiveDefault(ins, []ModelSeed{{ID: "a/1"}, {ID: "a/2"}}); got != "a/2" {
		t.Fatalf("refused first must be skipped, got %q", got)
	}
}

func TestWithOpencodeAccountKeepsInstanceSettings(t *testing.T) {
	ins := Instance{Type: TypeOpencode, Name: "x", OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir(), AllowHosted: true, Model: "opencode/big-pickle"}}
	acc, err := WithOpencodeAccount(ins, "a2")
	if err != nil || !acc.OpencodeConfig.AllowHosted || acc.OpencodeConfig.Model != "opencode/big-pickle" {
		t.Fatalf("account instance lost settings: %+v %v", acc.OpencodeConfig, err)
	}
	if acc.OpencodeConfig == ins.OpencodeConfig || ins.OpencodeConfig.DataDir == acc.OpencodeConfig.DataDir {
		t.Fatal("account must not alias or keep the base folder")
	}
	if _, err := WithOpencodeAccount(ins, "../x"); err == nil {
		t.Fatal("path-like account id must be refused")
	}
}

func TestOpencodeQuotaErrorRotates(t *testing.T) {
	withModelStateDir(t)
	RegisterModelSets(TypeOpencode, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOpencode, nil) })
	ins := &Instance{Type: TypeOpencode, Name: "rot-" + t.Name()}
	w := newModelTurnWatch(ins, EncodePin([]string{"openai", "auto"}, "openai/gpt-5"))
	// An answer that merely mentions 429 is not a quota hit.
	w.observe(`{"type":"text","part":{"text":"HTTP 429 means rate limit"}}`)
	if got := PickRotatingAccount(*ins, "openai", []string{"main", "a2"}); got != "main" {
		t.Fatalf("text mention rotated: %q", got)
	}
	w.observe(`{"type":"error","error":{"name":"APIError","data":{"message":"429 Too Many Requests: usage limit reached"}}}`)
	if got := PickRotatingAccount(*ins, "openai", []string{"main", "a2"}); got != "a2" {
		t.Fatalf("quota error must rotate to a2, got %q", got)
	}
}
