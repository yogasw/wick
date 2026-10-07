package slack

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sandboxAt points uploadSandboxRoot at a temp dir for one test and
// returns it, so a path= upload can be exercised without touching the
// real agents tree.
func sandboxAt(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	prev := uploadSandboxRoot
	uploadSandboxRoot = func() string { return root }
	t.Cleanup(func() { uploadSandboxRoot = prev })
	return root
}

// pdfBytes is a tiny but genuinely binary payload: it contains a NUL and a
// 0xFF byte, so anything that round-trips it through a Go string as UTF-8
// (the old content-only input) would corrupt it.
var pdfBytes = []byte("%PDF-1.4\n\x00\xff\n%%EOF\n")

func TestResolveUploadSource_Content(t *testing.T) {
	b, name, err := resolveUploadSource("", "", "hello world", "notes.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello world"), b)
	assert.Equal(t, "notes.txt", name)
}

func TestResolveUploadSource_ContentNeedsFilename(t *testing.T) {
	_, _, err := resolveUploadSource("", "", "hello", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filename is required")
}

func TestResolveUploadSource_Base64Binary(t *testing.T) {
	for _, enc := range []struct {
		name string
		in   string
	}{
		{"std", base64.StdEncoding.EncodeToString(pdfBytes)},
		{"raw-std", base64.RawStdEncoding.EncodeToString(pdfBytes)},
		{"url", base64.URLEncoding.EncodeToString(pdfBytes)},
		{"wrapped", base64.StdEncoding.EncodeToString(pdfBytes)[:4] + "\n" + base64.StdEncoding.EncodeToString(pdfBytes)[4:]},
	} {
		t.Run(enc.name, func(t *testing.T) {
			b, name, err := resolveUploadSource("", enc.in, "", "doc.pdf")
			require.NoError(t, err)
			assert.Equal(t, pdfBytes, b, "binary payload must survive base64 round-trip byte for byte")
			assert.Equal(t, "doc.pdf", name)
		})
	}
}

func TestResolveUploadSource_Base64Invalid(t *testing.T) {
	_, _, err := resolveUploadSource("", "not base64 !!!", "", "doc.pdf")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid base64")
}

func TestResolveUploadSource_PathReadsBytesAndDefaultsFilename(t *testing.T) {
	root := sandboxAt(t)
	p := filepath.Join(root, "projects", "p1", "files")
	require.NoError(t, os.MkdirAll(p, 0o755))
	f := filepath.Join(p, "report.pdf")
	require.NoError(t, os.WriteFile(f, pdfBytes, 0o644))

	b, name, err := resolveUploadSource(f, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, pdfBytes, b)
	assert.Equal(t, "report.pdf", name, "filename defaults to the path's base name")
}

func TestResolveUploadSource_PathExplicitFilenameWins(t *testing.T) {
	root := sandboxAt(t)
	f := filepath.Join(root, "report.pdf")
	require.NoError(t, os.WriteFile(f, pdfBytes, 0o644))

	_, name, err := resolveUploadSource(f, "", "", "laporan-mingguan.pdf")
	require.NoError(t, err)
	assert.Equal(t, "laporan-mingguan.pdf", name)
}

func TestResolveUploadSource_PathMustBeAbsolute(t *testing.T) {
	sandboxAt(t)
	_, _, err := resolveUploadSource("files/report.pdf", "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be absolute")
}

// A path outside the sandbox is the exfiltration case — one files:write
// scope must not become "post ~/.ssh/id_rsa to a channel".
func TestResolveUploadSource_PathOutsideSandboxRefused(t *testing.T) {
	sandboxAt(t)
	outside := filepath.Join(t.TempDir(), "id_rsa")
	require.NoError(t, os.WriteFile(outside, []byte("PRIVATE KEY"), 0o600))

	_, _, err := resolveUploadSource(outside, "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside")
}

// …and neither must a symlink planted inside the sandbox that points out.
func TestResolveUploadSource_SymlinkEscapeRefused(t *testing.T) {
	root := sandboxAt(t)
	outside := filepath.Join(t.TempDir(), "id_rsa")
	require.NoError(t, os.WriteFile(outside, []byte("PRIVATE KEY"), 0o600))
	link := filepath.Join(root, "innocent.pdf")
	require.NoError(t, os.Symlink(outside, link))

	_, _, err := resolveUploadSource(link, "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside")
}

func TestResolveUploadSource_PathDirectoryRefused(t *testing.T) {
	root := sandboxAt(t)
	dir := filepath.Join(root, "files")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	_, _, err := resolveUploadSource(dir, "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "directory")
}

func TestResolveUploadSource_ExclusiveInputs(t *testing.T) {
	_, _, err := resolveUploadSource("", "AAAA", "text", "f.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")

	_, _, err = resolveUploadSource("", "", "", "f.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one of path, content_base64, or content is required")
}

// captured records what the fake Slack saw across the three upload steps.
type captured struct {
	step1ContentType string
	step1Form        map[string]string
	uploadedBytes    []byte
	uploadedFilename string
	completeBody     map[string]any
}

// mockUploadSlack stands in for the three endpoints of the v2 upload flow.
// It mirrors the one behaviour that broke real uploads: files.getUploadURLExternal
// answers invalid_arguments unless the request is form-encoded.
func mockUploadSlack(t *testing.T, cap *captured) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			cap.step1ContentType = r.Header.Get("Content-Type")
			mt, _, _ := mime.ParseMediaType(cap.step1ContentType)
			if mt != "application/x-www-form-urlencoded" {
				_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_arguments"}`))
				return
			}
			require.NoError(t, r.ParseForm())
			cap.step1Form = map[string]string{}
			for k := range r.PostForm {
				cap.step1Form[k] = r.PostForm.Get(k)
			}
			_, _ = fmt.Fprintf(w, `{"ok":true,"upload_url":%q,"file_id":"F123"}`, srv.URL+"/upload")

		case "/upload":
			mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			require.NoError(t, err)
			require.Equal(t, "multipart/form-data", mt)
			mr := multipart.NewReader(r.Body, params["boundary"])
			part, err := mr.NextPart()
			require.NoError(t, err)
			cap.uploadedFilename = part.FileName()
			cap.uploadedBytes, err = io.ReadAll(part)
			require.NoError(t, err)
			_, _ = w.Write([]byte(`{"ok":true}`))

		case "/files.completeUploadExternal":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &cap.completeBody))
			_, _ = w.Write([]byte(`{"ok":true,"files":[{"id":"F123","name":"report.pdf","title":"report.pdf","permalink":"https://slack/files/F123","channels":["D1"]}]}`))

		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestUploadFile_PDFFromPath is the end-to-end regression: a real binary
// file, sent by path, must reach Slack byte-identical — and step 1 must be
// form-encoded, which is what made every upload fail with invalid_arguments.
func TestUploadFile_PDFFromPath(t *testing.T) {
	root := sandboxAt(t)
	f := filepath.Join(root, "report.pdf")
	require.NoError(t, os.WriteFile(f, pdfBytes, 0o644))

	cap := &captured{}
	srv := mockUploadSlack(t, cap)
	withBaseURL(t, srv.URL)

	c := newCtxWithInput(t, map[string]string{
		"path":            f,
		"channel_id":      "D1",
		"thread_ts":       "1788477166.714359",
		"initial_comment": "laporan",
	})
	out, err := uploadFile(c)
	require.NoError(t, err)

	assert.Contains(t, cap.step1ContentType, "application/x-www-form-urlencoded")
	assert.Equal(t, "report.pdf", cap.step1Form["filename"])
	assert.Equal(t, fmt.Sprint(len(pdfBytes)), cap.step1Form["length"], "length must be the real byte count")
	assert.Equal(t, pdfBytes, cap.uploadedBytes, "PDF bytes must arrive unmodified")
	assert.Equal(t, "report.pdf", cap.uploadedFilename)
	assert.Equal(t, "D1", cap.completeBody["channel_id"])
	assert.Equal(t, "1788477166.714359", cap.completeBody["thread_ts"])

	m, ok := out.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "F123", m["file_id"])
	assert.Equal(t, "https://slack/files/F123", m["permalink"])
}

func TestUploadFile_PDFFromBase64(t *testing.T) {
	cap := &captured{}
	srv := mockUploadSlack(t, cap)
	withBaseURL(t, srv.URL)

	c := newCtxWithInput(t, map[string]string{
		"filename":       "report.pdf",
		"content_base64": base64.StdEncoding.EncodeToString(pdfBytes),
		"channel_id":     "D1",
	})
	_, err := uploadFile(c)
	require.NoError(t, err)
	assert.Equal(t, pdfBytes, cap.uploadedBytes)
}

func TestUploadFile_EmptyContentRejectedBeforeSlack(t *testing.T) {
	root := sandboxAt(t)
	f := filepath.Join(root, "empty.pdf")
	require.NoError(t, os.WriteFile(f, nil, 0o644))

	cap := &captured{}
	srv := mockUploadSlack(t, cap)
	withBaseURL(t, srv.URL)

	_, err := uploadFile(newCtxWithInput(t, map[string]string{"path": f, "channel_id": "D1"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
	assert.Empty(t, cap.step1ContentType, "must not call Slack with a zero-length upload")
}

func TestUploadFile_ThreadTSNeedsChannel(t *testing.T) {
	_, err := uploadFile(newCtxWithInput(t, map[string]string{
		"filename":  "a.txt",
		"content":   "x",
		"thread_ts": "1.2",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel_id is required")
}

// TestResolveUploadSource_SwapAfterValidationRefused exercises the TOCTOU
// window directly: the probe fires between the pre-open stat and the open, and
// replaces the validated file with a symlink to a secret outside the sandbox —
// exactly the swap that the stat-then-ReadFile shape would have followed.
func TestResolveUploadSource_SwapAfterValidationRefused(t *testing.T) {
	root := sandboxAt(t)
	victim := filepath.Join(root, "report.pdf")
	require.NoError(t, os.WriteFile(victim, pdfBytes, 0o644))

	secret := filepath.Join(t.TempDir(), "id_rsa")
	require.NoError(t, os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600))

	prev := uploadRaceProbe
	uploadRaceProbe = func() {
		// One shot: swap the entry, then disarm so the retry-free path is clean.
		uploadRaceProbe = nil
		require.NoError(t, os.Remove(victim))
		require.NoError(t, os.Symlink(secret, victim))
	}
	t.Cleanup(func() { uploadRaceProbe = prev })

	b, _, err := resolveUploadSource(victim, "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "changed while it was being validated")
	assert.NotContains(t, string(b), "PRIVATE KEY", "the secret must never be read")
}

// TestResolveUploadSource_OverSizeLimitRefused covers the size ceiling on the
// path branch, which is now checked against the open handle rather than a
// separate stat of the name.
func TestResolveUploadSource_OverSizeLimitRefused(t *testing.T) {
	root := sandboxAt(t)
	big := filepath.Join(root, "big.bin")
	f, err := os.Create(big)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(int64(maxUploadBytes)+1))
	require.NoError(t, f.Close())

	_, _, err = resolveUploadSource(big, "", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over the")
}
