package host_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

const snapshotPath = "testdata/host_v1.snapshot"

// hostContract flattens host.proto into one line per RPC and per field so a
// snapshot diff shows exactly what changed.
func hostContract() []string {
	fd := hostpb.File_host_proto
	var out []string
	svcs := fd.Services()
	for i := 0; i < svcs.Len(); i++ {
		ms := svcs.Get(i).Methods()
		for j := 0; j < ms.Len(); j++ {
			m := ms.Get(j)
			out = append(out, fmt.Sprintf("rpc %s.%s(%s) %s", svcs.Get(i).Name(), m.Name(), m.Input().FullName(), m.Output().FullName()))
		}
	}
	var walk func(protoreflect.MessageDescriptors)
	walk = func(msgs protoreflect.MessageDescriptors) {
		for i := 0; i < msgs.Len(); i++ {
			m := msgs.Get(i)
			if m.IsMapEntry() {
				continue
			}
			fs := m.Fields()
			for j := 0; j < fs.Len(); j++ {
				f := fs.Get(j)
				typ := f.Kind().String()
				switch {
				case f.IsMap():
					typ = fmt.Sprintf("map<%s,%s>", f.MapKey().Kind(), f.MapValue().Kind())
				case f.Message() != nil:
					typ = string(f.Message().FullName())
				}
				if f.HasOptionalKeyword() {
					typ += " (explicit presence)"
				}
				out = append(out, fmt.Sprintf("field %s.%s=%d %s %s", m.FullName(), f.Name(), f.Number(), f.Cardinality(), typ))
			}
			walk(m.Messages())
		}
	}
	walk(fd.Messages())
	slices.Sort(out)
	return out
}

// TestHostProtoOnlyAdds enforces the wick.host.v1 compatibility rule: RPCs and
// fields may be added, never renumbered, renamed, retyped or removed. After an
// additive change, refresh the snapshot with WICK_UPDATE_HOST_SNAPSHOT=1; the
// refresh itself refuses to drop a line unless HOST_SNAPSHOT_FORCE=1 (a
// deliberate break, which belongs in wick.host.v2 instead).
func TestHostProtoOnlyAdds(t *testing.T) {
	cur := hostContract()
	raw, err := os.ReadFile(snapshotPath)
	if err != nil && !(os.IsNotExist(err) && os.Getenv("WICK_UPDATE_HOST_SNAPSHOT") == "1") {
		t.Fatalf("read snapshot: %v", err)
	}
	var missing []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" && !slices.Contains(cur, line) {
			missing = append(missing, line)
		}
	}
	if os.Getenv("WICK_UPDATE_HOST_SNAPSHOT") == "1" {
		if len(missing) > 0 && os.Getenv("HOST_SNAPSHOT_FORCE") != "1" {
			t.Fatalf("refusing to refresh: the change removes or alters %d contract line(s), e.g. %s", len(missing), missing[0])
		}
		if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(snapshotPath, []byte(strings.Join(cur, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, line := range missing {
		t.Errorf("host.proto broke the v1 contract (only additions allowed): %s", line)
	}
}
