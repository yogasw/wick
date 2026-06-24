//go:build linux

package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/blakesmith/ar"
)

// makeTestDeb builds a minimal .deb (ar → data.tar.gz → ./usr/bin/<app>)
// carrying payload as the inner binary, enough to exercise the extract
// path without invoking dpkg.
func makeTestDeb(t *testing.T, appName string, payload []byte) []byte {
	t.Helper()

	var dataBuf bytes.Buffer
	gz := gzip.NewWriter(&dataBuf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "./usr/bin/" + appName, Mode: 0o755, Size: int64(len(payload))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}

	var debBuf bytes.Buffer
	w := ar.NewWriter(&debBuf)
	if err := w.WriteGlobalHeader(); err != nil {
		t.Fatalf("ar header: %v", err)
	}
	for _, m := range []struct {
		name string
		data []byte
	}{
		{"debian-binary", []byte("2.0\n")},
		{"control.tar.gz", []byte{}}, // not parsed by extractInnerBinary
		{"data.tar.gz", dataBuf.Bytes()},
	} {
		if err := w.WriteHeader(&ar.Header{Name: m.name, Size: int64(len(m.data)), Mode: 0o644}); err != nil {
			t.Fatalf("ar member %s: %v", m.name, err)
		}
		if _, err := w.Write(m.data); err != nil {
			t.Fatalf("ar write %s: %v", m.name, err)
		}
	}
	return debBuf.Bytes()
}

// TestMaterializeInnerBinary verifies the Termux/no-dpkg apply path:
// a staged .deb is peeled to its inner ELF and written as a sibling file
// ready for swapUnix, without any package manager.
func TestMaterializeInnerBinary(t *testing.T) {
	dir := t.TempDir()
	want := []byte("\x7fELF-fake-binary-payload")
	deb := makeTestDeb(t, "myapp", want)

	debPath := filepath.Join(dir, "myapp-1.2.3.deb")
	if err := os.WriteFile(debPath, deb, 0o644); err != nil {
		t.Fatalf("write deb: %v", err)
	}

	u := &Updater{appName: "myapp"}
	out, err := u.materializeInnerBinary(debPath)
	if err != nil {
		t.Fatalf("materializeInnerBinary: %v", err)
	}
	if filepath.Ext(out) == ".deb" {
		t.Errorf("extracted path still has .deb suffix: %s", out)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted binary mismatch:\n got %q\nwant %q", got, want)
	}
	if fi, err := os.Stat(out); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("extracted binary not executable (mode=%v err=%v)", fi.Mode(), err)
	}
}

// TestInstallDirWritable confirms a writable temp dir passes and a
// nonexistent dir fails — the gate that keeps self-update from trying an
// in-place swap where it has no permission (instead of escalating).
func TestInstallDirWritable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "app")
	if !installDirWritable(exe) {
		t.Errorf("expected writable temp dir to pass")
	}
	missing := filepath.Join(dir, "does-not-exist", "app")
	if installDirWritable(missing) {
		t.Errorf("expected nonexistent dir to fail")
	}
}
