package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/yogasw/wick/internal/agents/provider/managedbin"
	"github.com/yogasw/wick/internal/userconfig"
)

// fakeManagedOMP lays out <data>/providers/bin/omp/{current,versions/<v>/omp}
// the way managedbin leaves it after an install.
func fakeManagedOMP(t *testing.T, ver string) string {
	t.Helper()
	dir := filepath.Join(ManagedBinRoot(), "omp")
	bin := filepath.Join(dir, "versions", ver, "omp")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(bin, []byte("#!/bin/sh\necho omp/"+ver+"\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "current"), []byte(ver+"\n"), 0o600)
	return bin
}

func TestManagedResolutionOrder(t *testing.T) {
	t.Setenv("WICK_DATA_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // nothing on PATH
	// omp's source registers from the omp package, linked into this test
	// binary by the external catalog_test.go import.
	if !ManagedEnabled(TypeOMP) {
		t.Skip("omp source not registered in this test binary")
	}
	bin := fakeManagedOMP(t, "18.4.3")
	ins := Instance{Type: TypeOMP, Name: "omp"}
	if p, src := ResolveBinarySource(ins); p != bin || src != BinSourceManaged {
		t.Fatalf("got %q %q", p, src)
	}
	ins.Binary = "/opt/omp"
	if p, src := ResolveBinarySource(ins); p != "/opt/omp" || src != BinSourceOverride {
		t.Fatalf("override lost: %q %q", p, src)
	}
}

func TestManagedBinariesConfigJSON(t *testing.T) {
	var c userconfig.ProvidersConfig
	in := `{"managed_binaries":{"keep_versions":3,"omp":{"enabled":false}}}`
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatal(err)
	}
	mb := c.ManagedBinaries
	if mb == nil || mb.KeepVersions != 3 || mb.Types["omp"].Enabled == nil || *mb.Types["omp"].Enabled {
		t.Fatalf("%+v", mb)
	}
	out, _ := json.Marshal(c)
	var back userconfig.ProvidersConfig
	if err := json.Unmarshal(out, &back); err != nil || back.ManagedBinaries.KeepVersions != 3 {
		t.Fatalf("round trip %s %v", out, err)
	}
}
