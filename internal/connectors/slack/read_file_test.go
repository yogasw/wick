package slack

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/pkg/connector"
)

// fakeSlackFile serves files.info for one file and its download URL.
// reportSize < 0 leaves size out of files.info; chunked hides
// Content-Length on the download.
type fakeSlackFile struct {
	name, mimetype string
	body           []byte
	reportSize     int
	chunked        bool
	downloads      atomic.Int32
}

func (f *fakeSlackFile) serve(t *testing.T) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/dl") {
			f.downloads.Add(1)
			w.Header().Set("Content-Type", f.mimetype)
			if f.chunked {
				w.(http.Flusher).Flush() // headers out before the body: no Content-Length
			}
			_, _ = w.Write(f.body)
			return
		}
		file := map[string]any{"id": "F1", "name": f.name, "mimetype": f.mimetype, "url_private_download": srv.URL + "/dl"}
		if f.reportSize >= 0 {
			file["size"] = f.reportSize
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "file": file})
	}))
	t.Cleanup(srv.Close)
	withBaseURL(t, srv.URL)
}

func runReadFile(t *testing.T, f *fakeSlackFile, input map[string]string) (any, error) {
	t.Helper()
	f.serve(t)
	in := map[string]string{"file": "F1"}
	for k, v := range input {
		in[k] = v
	}
	return readFile(newCtxWithInput(t, in))
}

func fileOutputOf(t *testing.T, out any) (map[string]any, connector.FileOutput) {
	t.Helper()
	m, ok := out.(map[string]any)
	require.True(t, ok)
	fo, ok := m[connector.FileOutputKey].(connector.FileOutput)
	require.True(t, ok, "result carries a file output")
	return m, fo
}

func TestReadFile_SmallBinaryStaysInline(t *testing.T) {
	png := append([]byte{0x89, 'P', 'N', 'G'}, bytes.Repeat([]byte{0}, 1024)...)
	out, err := runReadFile(t, &fakeSlackFile{name: "shot.png", mimetype: "image/png", body: png, reportSize: len(png)}, nil)
	require.NoError(t, err)
	m := out.(map[string]any)
	require.NotContains(t, m, connector.FileOutputKey)
	require.Equal(t, base64.StdEncoding.EncodeToString(png), m["content_base64"])
	require.Equal(t, false, m["is_text"])
}

func TestReadFile_BigBinaryGoesToFileWithoutBase64(t *testing.T) {
	body := bytes.Repeat([]byte{0x00, 0x01, 0xfe, 0xff}, (3<<20)/4)
	enc := base64.StdEncoding.EncodeToString(body)
	out, err := runReadFile(t, &fakeSlackFile{name: "clip.MP4", mimetype: "video/mp4", body: body, reportSize: len(body)}, nil)
	require.NoError(t, err)
	m, fo := fileOutputOf(t, out)
	require.Equal(t, "mp4", fo.Format)
	require.Equal(t, "clip.MP4", fo.Name)
	require.Empty(t, fo.Content)
	require.Empty(t, fo.ContentBase64, "raw bytes in-process, no base64 copy")
	require.Equal(t, body, fo.Data)

	// Everything but the file output (which the host strips) is metadata.
	require.NotContains(t, m, "content")
	require.NotContains(t, m, "content_base64")
	meta := map[string]any{}
	for k, v := range m {
		if k != connector.FileOutputKey {
			meta[k] = v
		}
	}
	b, _ := json.Marshal(meta)
	require.NotContains(t, string(b), enc[:64])
	// Across a plugin boundary the same output is encoded as content_base64.
	wire, err := json.Marshal(fo)
	require.NoError(t, err)
	require.Contains(t, string(wire), `"content_base64":"`+enc[:64])
	require.NotContains(t, string(wire), `"Data"`)
	require.Equal(t, len(body), m["size"])
}

func TestReadFile_SaveToArtifactsForcesFile(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	out, err := runReadFile(t, &fakeSlackFile{name: "shot", mimetype: "image/png", body: png, reportSize: len(png)}, map[string]string{"save_to_artifacts": "true"})
	require.NoError(t, err)
	m, fo := fileOutputOf(t, out)
	require.Equal(t, "png", fo.Format, "format from mimetype when the name has no extension")
	require.NotContains(t, m, "content_base64")

	// Small text: forced to a file too, as text (masked by the host).
	out, err = runReadFile(t, &fakeSlackFile{name: "notes.md", mimetype: "text/markdown", body: []byte("# hi"), reportSize: 4}, map[string]string{"save_to_artifacts": "true"})
	require.NoError(t, err)
	m, fo = fileOutputOf(t, out)
	require.Equal(t, "txt", fo.Format)
	require.Equal(t, "# hi", fo.Content)
	require.Empty(t, fo.ContentBase64)
	require.Empty(t, fo.Data)
	require.NotContains(t, m, "content")
}

func TestReadFile_BigTextGoesToFile(t *testing.T) {
	body := []byte(strings.Repeat("a,b\n", (300<<10)/4))
	out, err := runReadFile(t, &fakeSlackFile{name: "rows.csv", mimetype: "text/csv", body: body, reportSize: len(body)}, nil)
	require.NoError(t, err)
	_, fo := fileOutputOf(t, out)
	require.Equal(t, "csv", fo.Format)
	require.Equal(t, string(body), fo.Content)

	// Active-content text is saved as inert .txt, never as .html.
	html := []byte("<script>" + strings.Repeat("x", 300<<10) + "</script>")
	out, err = runReadFile(t, &fakeSlackFile{name: "page.html", mimetype: "text/html", body: html, reportSize: len(html)}, nil)
	require.NoError(t, err)
	_, fo = fileOutputOf(t, out)
	require.Equal(t, "txt", fo.Format)
}

func TestReadFile_UnsupportedBigBinaryRefused(t *testing.T) {
	big := bytes.Repeat([]byte{0xff, 0x00}, (3<<20)/2)
	for _, c := range []struct{ name, mt string }{
		{"setup.exe", "application/x-msdownload"},
		{"run.sh", "application/octet-stream"},
		{"tool.PY", "application/octet-stream"},
		{"noext", "application/octet-stream"},
		{"weird.!!", "application/octet-stream"},
	} {
		_, err := runReadFile(t, &fakeSlackFile{name: c.name, mimetype: c.mt, body: big, reportSize: len(big)}, nil)
		require.ErrorContains(t, err, "cannot be saved", c.name)
	}
	// Forced to a file, a small executable is refused too.
	_, err := runReadFile(t, &fakeSlackFile{name: "a.exe", mimetype: "application/x-msdownload", body: []byte{0xff}, reportSize: 1}, map[string]string{"save_to_artifacts": "true"})
	require.ErrorContains(t, err, "cannot be saved")

	// A type that is not denied is handed to the host, which applies the
	// admin's allowed list (svg is off it by default).
	out, err := runReadFile(t, &fakeSlackFile{name: "logo.svg", mimetype: "image/svg+xml", body: big, reportSize: len(big)}, nil)
	require.NoError(t, err)
	_, fo := fileOutputOf(t, out)
	require.Equal(t, "svg", fo.Format)
}

func TestReadFile_MaxBytes(t *testing.T) {
	body := bytes.Repeat([]byte{0xff}, 4096)

	// Reported size over the cap: refused before any download.
	f := &fakeSlackFile{name: "a.png", mimetype: "image/png", body: body, reportSize: len(body)}
	_, err := runReadFile(t, f, map[string]string{"max_bytes": "1000"})
	require.ErrorContains(t, err, "max_bytes")
	require.EqualValues(t, 0, f.downloads.Load())

	// No size reported: Content-Length catches it.
	f = &fakeSlackFile{name: "a.png", mimetype: "image/png", body: body, reportSize: -1}
	_, err = runReadFile(t, f, map[string]string{"max_bytes": "1000"})
	require.ErrorContains(t, err, "max_bytes")

	// No size, no Content-Length: the bounded read catches it.
	f = &fakeSlackFile{name: "a.png", mimetype: "image/png", body: body, reportSize: -1, chunked: true}
	_, err = runReadFile(t, f, map[string]string{"max_bytes": "1000"})
	require.ErrorContains(t, err, "max_bytes")

	// Without a host (plugin process) the default limit is 50 MB.
	f = &fakeSlackFile{name: "a.mp4", mimetype: "video/mp4", body: body, reportSize: 50<<20 + 1}
	_, err = runReadFile(t, f, nil)
	require.ErrorContains(t, err, "admin limit 50 MB")
	require.EqualValues(t, 0, f.downloads.Load())
	f = &fakeSlackFile{name: "a.mp4", mimetype: "video/mp4", body: body, reportSize: 50 << 20}
	_, err = runReadFile(t, f, nil)
	require.NoError(t, err)

}

// max_bytes cannot raise wick's admin limit: with the limit at 50 MB a
// 60 MB file is refused from files.info, before any download, even when the
// caller asks for 200 MiB. The message names the admin limit and does not
// suggest raising max_bytes.
func TestReadFile_MaxBytesCannotExceedAdminLimit(t *testing.T) {
	connector.SetMaxBinaryBytesFunc(func() int { return 50 << 20 })
	t.Cleanup(func() { connector.SetMaxBinaryBytesFunc(nil) })

	f := &fakeSlackFile{name: "big.mp4", mimetype: "video/mp4", body: []byte{0}, reportSize: 60 << 20}
	_, err := runReadFile(t, f, map[string]string{"max_bytes": fmt.Sprint(200 << 20)})
	require.ErrorContains(t, err, "over the admin limit 50 MB")
	require.NotContains(t, err.Error(), "raise")
	require.EqualValues(t, 0, f.downloads.Load(), "nothing downloaded")

	// Size not reported: the stream is cut at the admin limit, same message.
	connector.SetMaxBinaryBytesFunc(func() int { return 1 << 20 })
	f = &fakeSlackFile{name: "big.mp4", mimetype: "video/mp4", body: make([]byte, 2<<20), reportSize: -1, chunked: true}
	_, err = runReadFile(t, f, map[string]string{"max_bytes": fmt.Sprint(200 << 20)})
	require.ErrorContains(t, err, "over the admin limit 1 MB")
	require.NotContains(t, err.Error(), "raise")
}

// A text file that is not UTF-8 (a Latin-1 CSV) over the inline limit is
// handed over as raw bytes under its own .csv format, not refused.
func TestReadFile_Latin1CSVGoesToFileAsBytes(t *testing.T) {
	row := []byte("id;caf\xe9;na\xefve\n")
	body := bytes.Repeat(row, (3<<20)/len(row))
	out, err := runReadFile(t, &fakeSlackFile{name: "export.csv", mimetype: "text/csv", body: body, reportSize: len(body)}, nil)
	require.NoError(t, err)
	m, fo := fileOutputOf(t, out)
	require.Equal(t, "csv", fo.Format)
	require.Equal(t, body, fo.Data)
	require.Empty(t, fo.Content)
	require.Equal(t, false, m["is_text"])
}
