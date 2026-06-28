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
