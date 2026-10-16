package plugin

import (
	"encoding/json"
	"testing"
)

func TestDumpManifestIsModuleJSON(t *testing.T) {
	got, err := DumpManifest(sampleModule())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(sampleModule())
	if string(got) != string(want) {
		t.Fatalf("dump-manifest drift")
	}
}

func TestDumpManifestRejectsInvalidVisibleWhen(t *testing.T) {
	mod := sampleModule()
	mod.Operations[0].Ops[0].VisibleWhen = "missing:x"
	if _, err := DumpManifest(mod); err == nil {
		t.Fatal("want error for visible_when on unknown field")
	}
	if _, err := BuildSelfManifest(mod, ""); err == nil {
		t.Fatal("BuildSelfManifest must reject it too")
	}
}
