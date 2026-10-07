package provider

import (
	"testing"
)

func TestEffectiveLiveModels(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	ins := Instance{Type: TypeOMP, Name: "lm-yoga", LiveModels: true, OMPConfig: &OMPConfig{Profile: "wick-yoga"}}
	all := []ModelSeed{{ID: "openai-codex/gpt-5.5"}, {ID: "openai-codex/gpt-5.6-luna"}, {ID: "openai-codex/gpt-5.4-mini"}}

	// The live 0.1.398 case: the account refused gpt-5.5 (model_not_found).
	MarkModelUnavailable(ins, "openai-codex", "openai-codex/gpt-5.5", "")
	rows := EffectiveLiveModels(ins, all)
	if len(rows) != 3 || !rows[0].Unavailable || rows[0].Default {
		t.Fatalf("refused row: %+v", rows[0])
	}
	if !rows[1].Default {
		t.Fatalf("default must be the first usable row: %+v", rows)
	}

	// A chosen Default wins over list order, unless refused.
	ins.LiveModelDefault = "openai-codex/gpt-5.4-mini"
	if rows := EffectiveLiveModels(ins, all); rows[0].ID != "openai-codex/gpt-5.4-mini" || !rows[0].Default {
		t.Fatalf("chosen default: %+v", rows)
	}
	ins.LiveModelDefault = "openai-codex/gpt-5.5"
	rows = EffectiveLiveModels(ins, all)
	for _, r := range rows {
		if r.Default && r.ID == "openai-codex/gpt-5.5" {
			t.Fatalf("a refused Default became the effective default: %+v", rows)
		}
	}
	// The saved filter narrows the list the same way for every reader.
	ins.LiveModelDefault, ins.LiveModelFilter = "", "mini"
	if rows := EffectiveLiveModels(ins, all); len(rows) != 1 || rows[0].ID != "openai-codex/gpt-5.4-mini" {
		t.Fatalf("filter: %+v", rows)
	}
	// The unfiltered preview list carries the mark too.
	if m := MarkLiveModels(ins, all); !m[0].Unavailable || m[1].Unavailable {
		t.Fatalf("marks: %+v", m)
	}
}

// Refresh no longer forgets refusals; only an explicit re-check (or the
// model working) does.
func TestClearModelRefusal(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	ins := Instance{Type: TypeOMP, Name: "lm-clear"}
	MarkModelUnavailable(ins, "openai-codex", "openai-codex/gpt-5.5", "plan free")
	MarkModelUnavailable(ins, "openai-codex#2", "openai-codex/gpt-5.5", "")
	if !ClearModelRefusal(ins, "openai-codex/gpt-5.5") {
		t.Fatal("nothing cleared")
	}
	for _, k := range []string{"openai-codex", "openai-codex#2"} {
		if _, bad := ModelUnavailable(ins, k, "openai-codex/gpt-5.5"); bad {
			t.Fatalf("still refused on %s", k)
		}
	}
	if ClearModelRefusal(ins, "openai-codex/gpt-5.5") {
		t.Fatal("second clear reported a change")
	}
}

// The real omp line (RPC and -p print the same message events) is recorded
// as a refusal for the account.
func TestWatchRecordsOMPModelNotFound(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	RegisterModelSets(TypeOMP, fakeSets{}) // logintty registers it in the binary
	t.Cleanup(func() { RegisterModelSets(TypeOMP, nil) })
	ins := Instance{Type: TypeOMP, Name: "lm-omp", OMPConfig: &OMPConfig{Profile: "wick-yoga"}}
	w := newModelTurnWatch(&ins, "")
	w.observe(`{"message":{"role":"assistant","content":[],"api":"openai-codex-responses","provider":"openai-codex","model":"gpt-5.5","stopReason":"error","errorMessage":"Codex error event: The model ` + "`gpt-5.5`" + ` does not exist or you do not have access to it. (code=model_not_found)"},"type":"message_end"}`)
	if _, bad := ModelUnavailable(ins, "openai-codex", "openai-codex/gpt-5.5"); !bad {
		t.Fatal("omp model_not_found not recorded")
	}
}

// opencode's error frame does not name the model: the spawn's --model
// does, so the refusal lands on the model that actually ran.
func TestWatchRecordsOpencodeModelNotFoundFromArgv(t *testing.T) {
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	RegisterModelSets(TypeOpencode, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOpencode, nil) })
	ins := Instance{Type: TypeOpencode, Name: "lm-oc", OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir()}}
	w := newModelTurnWatch(&ins, "")
	w.seedModelFromArgv([]string{"run", "--format", "json", "--model", "openai/gpt-5.5"})
	w.observe(`{"type":"error","timestamp":1,"sessionID":"ses_1","error":{"name":"APIError","data":{"message":"The model ` + "`gpt-5.5`" + ` does not exist or you do not have access to it.","statusCode":404}}}`)
	if _, bad := ModelUnavailable(ins, "openai", "openai/gpt-5.5"); !bad {
		t.Fatal("opencode model_not_found not recorded")
	}
	// A pin always wins over the argv.
	w2 := newModelTurnWatch(&ins, "openai/o5")
	w2.seedModelFromArgv([]string{"--model", "openai/gpt-5.5"})
	if w2.model != "openai/o5" {
		t.Fatalf("pin overridden: %q", w2.model)
	}
}

// opencode Zen's refusal text (gemini-3-flash, session aa6e34f5) is an
// access refusal: the model gets marked "not available".
func TestModelAccessDisabledIsRefusal(t *testing.T) {
	if !IsModelAccessError("Upstream request failed: Model access is disabled") {
		t.Fatal("not recognised")
	}
	t.Cleanup(SetModelStateDirForTest(t.TempDir()))
	RegisterModelSets(TypeOpencode, fakeSets{})
	t.Cleanup(func() { RegisterModelSets(TypeOpencode, nil) })
	ins := Instance{Type: TypeOpencode, Name: "lm-zen", OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir()}}
	w := newModelTurnWatch(&ins, "opencode/gemini-3-flash")
	w.observe(`{"type":"error","timestamp":1,"sessionID":"s","error":{"name":"UnknownError","data":{"message":"Upstream request failed: Model access is disabled"}}}`)
	if _, bad := ModelUnavailable(ins, "opencode", "opencode/gemini-3-flash"); !bad {
		t.Fatal("refusal not recorded")
	}
}
