package connectors

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/pkg/connector"
)

func TestExtractFileOutput(t *testing.T) {
	cwd := t.TempDir()
	SetArtifactDirResolver(func(id string) (string, error) {
		require.Equal(t, "sess-1", id)
		return cwd, nil
	})
	t.Cleanup(func() { SetArtifactDirResolver(nil) })

	// No file: unchanged.
	out, err := extractFileOutput(`{"rows":1}`, "sess-1")
	require.NoError(t, err)
	require.Equal(t, `{"rows":1}`, out)

	resp, _ := json.Marshal(connector.WithFile(map[string]any{"rows": 2}, connector.FileOutput{
		Name: "../../etc/apps report", Format: "csv", Content: "id,name\n1,a\n2,b\n",
	}))
	out, err = extractFileOutput(string(resp), "sess-1")
	require.NoError(t, err)
	require.NotContains(t, out, "1,a", "file content never stays in the result")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	ref := got[connector.FileRefKey].(map[string]any)
	require.Equal(t, "artifacts/apps-report.csv", ref["path"], "name sanitized, folder created, no traversal")
	b, err := os.ReadFile(filepath.Join(cwd, "artifacts", "apps-report.csv"))
	require.NoError(t, err)
	require.Equal(t, "id,name\n1,a\n2,b\n", string(b))

	// Same name again: a suffix, never an overwrite.
	out, err = extractFileOutput(string(resp), "sess-1")
	require.NoError(t, err)
	require.True(t, strings.Contains(out, "artifacts/apps-report-2.csv"), out)

	// No session, bad format: refused.
	_, err = extractFileOutput(string(resp), "")
	require.Error(t, err)
	bad, _ := json.Marshal(connector.WithFile(nil, connector.FileOutput{Name: "x", Format: "exe", Content: "MZ"}))
	_, err = extractFileOutput(string(bad), "sess-1")
	require.Error(t, err)
}

// artifactSession points the resolver at a fresh temp dir for one test.
func artifactSession(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	SetArtifactDirResolver(func(string) (string, error) { return cwd, nil })
	t.Cleanup(func() { SetArtifactDirResolver(nil) })
	return cwd
}

func artifactNames(t *testing.T, cwd string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(cwd, "artifacts"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteArtifactBinaryRoundTrip(t *testing.T) {
	cwd := artifactSession(t)
	blob := make([]byte, 3<<20+17)
	_, err := rand.Read(blob)
	require.NoError(t, err)
	enc := base64.StdEncoding.EncodeToString(blob)

	resp, _ := json.Marshal(connector.WithFile(map[string]any{"file_id": "F1"}, connector.FileOutput{
		Name: "clip.mp4", Format: "mp4", ContentBase64: enc,
	}))
	out, err := extractFileOutput(string(resp), "sess-1")
	require.NoError(t, err)
	require.NotContains(t, out, enc[:64], "the encoded bytes never stay in the result")
	require.NotContains(t, out, "content_base64")

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	ref := got[connector.FileRefKey].(map[string]any)
	require.Equal(t, "artifacts/clip.mp4", ref["path"])
	require.EqualValues(t, len(blob), ref["bytes"], "bytes = decoded length, not base64 length")

	b, err := os.ReadFile(filepath.Join(cwd, "artifacts", "clip.mp4"))
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(blob), sha256.Sum256(b), "raw bytes on disk, not base64 text")

	// URL-safe, unpadded, line-wrapped base64 decodes too.
	small := []byte{0xfb, 0xff, 0xfe, 0x00, 0x01}
	ref2, err := writeArtifact("sess-1", connector.FileOutput{Name: "x", Format: "png", ContentBase64: base64.RawURLEncoding.EncodeToString(small) + "\n"})
	require.NoError(t, err)
	b, err = os.ReadFile(filepath.Join(cwd, ref2.Path))
	require.NoError(t, err)
	require.Equal(t, small, b)
}

func TestWriteArtifactNameTraversal(t *testing.T) {
	cwd := artifactSession(t)
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG"))
	// In order: names that sanitise to nothing share "export" and take
	// suffixes.
	cases := []struct{ name, want string }{
		{"../../etc/passwd", "artifacts/passwd.png"},
		{"/etc/shadow", "artifacts/shadow.png"},
		{"a\x00b/../c", "artifacts/c.png"},
		{"nul\x00byte", "artifacts/nul-byte.png"},
		{"..", "artifacts/export.png"},
		{"", "artifacts/export-2.png"},
		{`..\..\windows\system32\evil`, "artifacts/export-3.png"},
		{"résumé 日本.png", "artifacts/r-sum.png"},
		{strings.Repeat("a", 500), "artifacts/" + strings.Repeat("a", 80) + ".png"},
	}
	for _, c := range cases {
		ref, err := writeArtifact("s", connector.FileOutput{Name: c.name, Format: "png", ContentBase64: png})
		require.NoError(t, err, c.name)
		require.Equal(t, c.want, ref.Path, "name %q", c.name)
	}

	// Everything landed inside artifacts/ and nothing else appeared in cwd.
	top, err := os.ReadDir(cwd)
	require.NoError(t, err)
	require.Len(t, top, 1)
	require.Equal(t, "artifacts", top[0].Name())
}

func TestWriteArtifactRejects(t *testing.T) {
	cwd := artifactSession(t)
	ok := base64.StdEncoding.EncodeToString([]byte("data"))
	cases := map[string]connector.FileOutput{
		"sh":            {Name: "x", Format: "sh", ContentBase64: ok},
		"exe":           {Name: "x", Format: "exe", ContentBase64: ok},
		"html":          {Name: "x", Format: "html", Content: "<script>"},
		"htm":           {Name: "x", Format: "htm", Content: "<script>"},
		"svg":           {Name: "x", Format: "svg", ContentBase64: ok},
		"js":            {Name: "x", Format: "js", Content: "alert(1)"},
		"py":            {Name: "x", Format: "py", Content: "print(1)"},
		"php":           {Name: "x", Format: "php", Content: "<?php"},
		"bat":           {Name: "x", Format: "BAT", ContentBase64: ok},
		"upper svg":     {Name: "x", Format: "SVG", ContentBase64: ok},
		"empty format":  {Name: "x", Format: "", ContentBase64: ok},
		"dot format":    {Name: "x", Format: ".png", ContentBase64: ok},
		"both":          {Name: "x", Format: "png", Content: "a", ContentBase64: ok},
		"neither bin":   {Name: "x", Format: "png"},
		"exe space":     {Name: "x", Format: "exe ", ContentBase64: ok},
		"dotted exe":    {Name: "x", Format: ".exe.", ContentBase64: ok},
		"data and text": {Name: "x", Format: "csv", Content: "a", Data: []byte("b")},
		"html as png":   {Name: "x", Format: "png", ContentBase64: base64.StdEncoding.EncodeToString([]byte("<html><script>alert(1)</script>"))},
		"text for bin":  {Name: "x", Format: "pdf", Content: "%PDF"},
		"invalid b64":   {Name: "x", Format: "png", ContentBase64: "!!!not base64!!!"},
		"truncated b64": {Name: "x", Format: "png", ContentBase64: "QUJD$"},
	}
	for label, f := range cases {
		_, err := writeArtifact("s", f)
		require.Error(t, err, label)
	}
	require.Empty(t, artifactNames(t, cwd), "nothing written for a refused file")

	// Uppercase variants of an allowed type are normalised, not refused.
	ref, err := writeArtifact("s", connector.FileOutput{Name: "x", Format: "PNG", ContentBase64: ok})
	require.NoError(t, err)
	require.Equal(t, "artifacts/x.png", ref.Path)
}

func TestWriteArtifactBinaryCapBeforeDecode(t *testing.T) {
	cwd := artifactSession(t)
	// Not valid base64 at all: if the cap ran after decoding, this would
	// fail with "not valid base64" instead of the size error.
	huge := strings.Repeat("!", base64.StdEncoding.EncodedLen(maxBinaryOutputBytes())+16)
	_, err := writeArtifact("s", connector.FileOutput{Name: "big", Format: "mp4", ContentBase64: huge})
	require.Error(t, err)
	require.Contains(t, err.Error(), "limit")
	require.Empty(t, artifactNames(t, cwd))
}

func TestWriteArtifactNoSession(t *testing.T) {
	ok := base64.StdEncoding.EncodeToString([]byte("data"))
	f := connector.FileOutput{Name: "x", Format: "png", ContentBase64: ok}

	SetArtifactDirResolver(nil)
	_, err := writeArtifact("s", f)
	require.ErrorContains(t, err, "no agent sessions here")
	require.ErrorContains(t, err, "Run it from an agent session")

	cwd := artifactSession(t)
	_, err = writeArtifact("", f)
	require.ErrorContains(t, err, "no agent session")
	require.ErrorContains(t, err, "2 MB")
	_, err = writeArtifact("  ", f)
	require.Error(t, err)
	require.Empty(t, artifactNames(t, cwd))

	SetArtifactDirResolver(func(string) (string, error) { return "", errors.New("unknown session") })
	_, err = writeArtifact("s", f)
	require.ErrorContains(t, err, "working directory")
}

func TestWriteArtifactNeverOverwrites(t *testing.T) {
	cwd := artifactSession(t)
	dir := filepath.Join(cwd, "artifacts")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shot.png"), []byte("original"), 0o644))

	ref, err := writeArtifact("s", connector.FileOutput{Name: "shot.png", Format: "png", ContentBase64: base64.StdEncoding.EncodeToString([]byte("new"))})
	require.NoError(t, err)
	require.Equal(t, "artifacts/shot-2.png", ref.Path)
	b, _ := os.ReadFile(filepath.Join(dir, "shot.png"))
	require.Equal(t, "original", string(b))

	// A symlink sitting at the target name is not followed either.
	outside := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(outside, []byte("keep"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.png")))
	ref, err = writeArtifact("s", connector.FileOutput{Name: "link", Format: "png", ContentBase64: base64.StdEncoding.EncodeToString([]byte("new"))})
	require.NoError(t, err)
	require.Equal(t, "artifacts/link-2.png", ref.Path)
	b, _ = os.ReadFile(outside)
	require.Equal(t, "keep", string(b))
}

func TestWriteArtifactRefusesSymlinkedDir(t *testing.T) {
	cwd := artifactSession(t)
	target := t.TempDir()
	require.NoError(t, os.Symlink(target, filepath.Join(cwd, "artifacts")))

	_, err := writeArtifact("s", connector.FileOutput{Name: "x", Format: "png", ContentBase64: base64.StdEncoding.EncodeToString([]byte("data"))})
	require.ErrorContains(t, err, "symlink")
	entries, _ := os.ReadDir(target)
	require.Empty(t, entries, "nothing written through the symlink")
}

func TestWriteArtifactPartialWriteRemoved(t *testing.T) {
	cwd := artifactSession(t)
	orig := artifactWrite
	artifactWrite = func(fh *os.File, b []byte) (int, error) {
		n, _ := fh.Write(b[:len(b)/2])
		return n, errors.New("disk full")
	}
	t.Cleanup(func() { artifactWrite = orig })

	_, err := writeArtifact("s", connector.FileOutput{Name: "x", Format: "pdf", ContentBase64: base64.StdEncoding.EncodeToString(make([]byte, 4096))})
	require.ErrorContains(t, err, "disk full")
	require.Empty(t, artifactNames(t, cwd), "the half-written file is removed")
}

// adminFileSettings installs a mutable file_io.* setting store for one test.
func adminFileSettings(t *testing.T) map[string]string {
	t.Helper()
	vals := map[string]string{}
	SetFileSettingLoader(func(key string) string { return vals[key] })
	t.Cleanup(func() { SetFileSettingLoader(nil) })
	return vals
}

func TestWriteArtifactAdminFormats(t *testing.T) {
	cwd := artifactSession(t)
	vals := adminFileSettings(t)
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	pngBytes := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	zipBytes := []byte("PK\x03\x04\x14\x00\x00\x00\x08\x00rest-of-an-odt")

	// Default list: png in, svg/html out.
	_, err := writeArtifact("s", connector.FileOutput{Name: "a", Format: "png", ContentBase64: b64(pngBytes)})
	require.NoError(t, err)
	_, err = writeArtifact("s", connector.FileOutput{Name: "a", Format: "svg", ContentBase64: b64([]byte("<svg/>"))})
	require.Error(t, err)

	// The admin adds formats: they are accepted right away, and what is
	// no longer listed is refused, with no restart in between.
	vals[configs.KeyFileAllowedFormats] = "png, .MD,html,odt"
	ref, err := writeArtifact("s", connector.FileOutput{Name: "notes", Format: "md", Content: "# hi"})
	require.NoError(t, err)
	require.Equal(t, "artifacts/notes.md", ref.Path)
	_, err = writeArtifact("s", connector.FileOutput{Name: "page", Format: "html", Content: "<html><body>x</body></html>"})
	require.NoError(t, err)
	// A zip-based format the sniffer cannot place goes on its extension.
	ref, err = writeArtifact("s", connector.FileOutput{Name: "doc", Format: "odt", ContentBase64: b64(zipBytes)})
	require.NoError(t, err)
	got, _ := os.ReadFile(filepath.Join(cwd, ref.Path))
	require.Equal(t, zipBytes, got)
	_, err = writeArtifact("s", connector.FileOutput{Name: "a", Format: "jpg", ContentBase64: b64(pngBytes)})
	require.ErrorContains(t, err, "not allowed")

	// An admin-added format whose content sniffs as another type is refused.
	_, err = writeArtifact("s", connector.FileOutput{Name: "fake", Format: "html", ContentBase64: b64(pngBytes)})
	require.ErrorContains(t, err, "looks like image/png")
	_, err = writeArtifact("s", connector.FileOutput{Name: "fake", Format: "md", Content: "<html><script>x</script></html>"})
	require.ErrorContains(t, err, "looks like text/html")
	_, err = writeArtifact("s", connector.FileOutput{Name: "empty", Format: "md"})
	require.Error(t, err)

	// Denied formats stay refused even when the admin lists them.
	vals[configs.KeyFileAllowedFormats] = "exe,sh,py,js,php,png"
	for _, f := range []string{"exe", "sh", "py", "js", "php", "EXE"} {
		_, err = writeArtifact("s", connector.FileOutput{Name: "x", Format: f, ContentBase64: b64([]byte("MZ"))})
		require.ErrorContains(t, err, "never written", f)
	}
	_, err = writeArtifact("s", connector.FileOutput{Name: "a", Format: "png", ContentBase64: b64(pngBytes)})
	require.NoError(t, err)

	// Empty, garbage, or only-denied settings fall back to the default list.
	for _, v := range []string{"", " , ,", "!!,@@,<script>", "exe,dll,sh"} {
		vals[configs.KeyFileAllowedFormats] = v
		_, err = writeArtifact("s", connector.FileOutput{Name: "a", Format: "jpg", ContentBase64: b64([]byte("\xff\xd8\xff"))})
		require.NoError(t, err, "setting %q", v)
		_, err = writeArtifact("s", connector.FileOutput{Name: "a", Format: "md", Content: "x"})
		require.Error(t, err, "setting %q", v)
	}

	for _, n := range artifactNames(t, cwd) {
		for _, bad := range []string{".exe", ".sh", ".py", ".js", ".php", ".svg"} {
			require.False(t, strings.HasSuffix(n, bad), n)
		}
	}
}

func TestWriteArtifactAdminMaxBinary(t *testing.T) {
	artifactSession(t)
	vals := adminFileSettings(t)

	for v, want := range map[string]int{"": 50, "abc": 50, "-3": 50, "0": 50, "1": 1, "120": 120, "999": 200, "200": 200} {
		vals[configs.KeyFileMaxBinaryMB] = v
		require.Equal(t, want<<20, maxBinaryOutputBytes(), "setting %q", v)
	}

	// Read per call: lowering it refuses the next write without a restart.
	blob := base64.StdEncoding.EncodeToString(make([]byte, 2<<20))
	vals[configs.KeyFileMaxBinaryMB] = "3"
	_, err := writeArtifact("s", connector.FileOutput{Name: "a", Format: "pdf", ContentBase64: blob})
	require.NoError(t, err)
	vals[configs.KeyFileMaxBinaryMB] = "1"
	_, err = writeArtifact("s", connector.FileOutput{Name: "a", Format: "pdf", ContentBase64: blob})
	require.ErrorContains(t, err, "limit")
}

// A symlink swapped in for artifacts/ while writes are in flight never
// leads a file outside the session folder (os.Root on every step).
func TestWriteArtifactSymlinkSwapRace(t *testing.T) {
	cwd := artifactSession(t)
	outside := t.TempDir()
	art := filepath.Join(cwd, "artifacts")
	parked := filepath.Join(cwd, "parked")
	require.NoError(t, os.Mkdir(art, 0o755))

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if os.Rename(art, parked) == nil {
				_ = os.Symlink(outside, art)
				_ = os.Remove(art)
				_ = os.Rename(parked, art)
			}
		}
	}()
	data := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))
	for i := 0; i < 400; i++ {
		_, _ = writeArtifact("s", connector.FileOutput{Name: "settings.local", Format: "png", ContentBase64: data})
		_, _ = writeArtifact("s", connector.FileOutput{Name: "settings.local", Format: "json", Content: `{"hooks":{}}`})
	}
	close(stop)
	<-done

	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing written through the swapped symlink")
}

func TestWriteArtifactTextAsBytes(t *testing.T) {
	cwd := artifactSession(t)
	latin1 := []byte("id;caf\xe9\n1;na\xefve\n")

	ref, err := writeArtifact("s", connector.FileOutput{Name: "export", Format: "csv", ContentBase64: base64.StdEncoding.EncodeToString(latin1)})
	require.NoError(t, err)
	got, _ := os.ReadFile(filepath.Join(cwd, ref.Path))
	require.Equal(t, latin1, got, "raw bytes, not re-encoded")

	ref, err = writeArtifact("s", connector.FileOutput{Name: "export", Format: "csv", Data: latin1})
	require.NoError(t, err)
	require.Equal(t, "artifacts/export-2.csv", ref.Path)
	got, _ = os.ReadFile(filepath.Join(cwd, ref.Path))
	require.Equal(t, latin1, got)

	fi, err := os.Stat(filepath.Join(cwd, ref.Path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

func TestWriteArtifactTextFormatsAlwaysAllowed(t *testing.T) {
	artifactSession(t)
	vals := adminFileSettings(t)
	vals[configs.KeyFileAllowedFormats] = "png"
	for _, f := range []string{"csv", "tsv", "json", "ndjson", "txt"} {
		_, err := writeArtifact("s", connector.FileOutput{Name: "x", Format: f, Content: "a"})
		require.NoError(t, err, f)
	}
	_, err := writeArtifact("s", connector.FileOutput{Name: "x", Format: "pdf", ContentBase64: "JVBERg=="})
	require.ErrorContains(t, err, "not allowed")
}

func TestWriteArtifactNameTrimAfterCut(t *testing.T) {
	artifactSession(t)
	ref, err := writeArtifact("s", connector.FileOutput{Name: strings.Repeat("a", 79) + "-bbbb.png", Format: "png", ContentBase64: "iVBORw0KGgo="})
	require.NoError(t, err)
	require.Equal(t, "artifacts/"+strings.Repeat("a", 79)+".png", ref.Path)
}

func TestWriteArtifactWrappedBase64AtCap(t *testing.T) {
	artifactSession(t)
	vals := adminFileSettings(t)
	vals[configs.KeyFileMaxBinaryMB] = "1"
	blob := make([]byte, 1<<20)
	_, _ = rand.Read(blob)
	enc := base64.StdEncoding.EncodeToString(blob)
	var wrapped strings.Builder
	for i := 0; i < len(enc); i += 76 {
		wrapped.WriteString(enc[i:min(i+76, len(enc))])
		wrapped.WriteString("\r\n")
	}
	ref, err := writeArtifact("s", connector.FileOutput{Name: "x", Format: "zip", ContentBase64: wrapped.String()})
	require.NoError(t, err, "line breaks do not count toward the size")
	require.Equal(t, 1<<20, ref.Bytes)
}
