package custom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// withSandbox points multipartSandboxRoot at a fresh temp dir for one test.
func withSandbox(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	prev := multipartSandboxRoot
	multipartSandboxRoot = func() string { return root }
	t.Cleanup(func() { multipartSandboxRoot = prev })
	return root
}

func TestExecuteHTTPMultipartSendsFileAndFields(t *testing.T) {
	root := withSandbox(t)
	file := filepath.Join(root, "projects", "p1", "files", "note.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("hello upload"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotField, gotName, gotData, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotField = r.FormValue("caption")
		gotHeader = r.Header.Get("X-App")
		f, h, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		gotName, gotData = h.Filename, string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":{"file":{"url":"https://cdn.example.com/note.txt"}}}`))
	}))
	defer srv.Close()

	recipe := OpRequest{
		Method:      "POST",
		URLTemplate: "{{.cfg.base_url}}/upload",
		Headers:     map[string]string{"X-App": "{{.cfg.app_id}}"},
		Multipart: &MultipartSpec{
			Fields: map[string]string{"caption": "{{.in.caption}}", "skipped": "{{.in.missing_ok}}"},
			Files:  map[string]string{"file": "{{.in.path}}"},
		},
	}
	cfg := map[string]string{"base_url": srv.URL, "app_id": "acme"}
	in := map[string]string{"path": file, "caption": "hi", "missing_ok": ""}
	c := connector.NewCtx(context.Background(), "i", cfg, in, srv.Client(), nil, nil)

	res, err := executeHTTP(c, recipe, []DefField{{Key: "base_url"}, {Key: "app_id"}}, []string{"path", "caption", "missing_ok"})
	if err != nil {
		t.Fatalf("executeHTTP: %v", err)
	}
	if gotName != "note.txt" || gotData != "hello upload" {
		t.Errorf("file part = %q / %q", gotName, gotData)
	}
	if gotField != "hi" || gotHeader != "acme" {
		t.Errorf("caption = %q, header = %q", gotField, gotHeader)
	}
	m, _ := res.(map[string]any)
	if m == nil || m["results"] == nil {
		t.Errorf("response not decoded: %#v", res)
	}
}

func TestBuildMultipartRefusesPathOutsideSandbox(t *testing.T) {
	withSandbox(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := &MultipartSpec{Files: map[string]string{"file": "{{.in.path}}"}}
	_, _, err := buildMultipart(spec, nil, map[string]string{"path": outside})
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("want outside-sandbox error, got %v", err)
	}
}

func TestBuildMultipartRefusesRelativeAndMissingPath(t *testing.T) {
	withSandbox(t)
	spec := &MultipartSpec{Files: map[string]string{"file": "{{.in.path}}"}}
	if _, _, err := buildMultipart(spec, nil, map[string]string{"path": "files/a.txt"}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("relative path: got %v", err)
	}
	if _, _, err := buildMultipart(spec, nil, map[string]string{"path": ""}); err == nil || !strings.Contains(err.Error(), "no file path") {
		t.Errorf("empty path: got %v", err)
	}
}

func TestBuildMultipartRefusesSymlinkOut(t *testing.T) {
	root := withSandbox(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlink not supported:", err)
	}
	spec := &MultipartSpec{Files: map[string]string{"file": "{{.in.path}}"}}
	if _, _, err := buildMultipart(spec, nil, map[string]string{"path": link}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("want outside-sandbox error for symlink, got %v", err)
	}
}

func TestOpRequestValidateMultipart(t *testing.T) {
	base := OpRequest{Method: "POST", URLTemplate: "https://api.example.com/upload"}

	ok := base
	ok.Multipart = &MultipartSpec{Files: map[string]string{"file": "{{.in.path}}"}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid multipart rejected: %v", err)
	}

	both := ok
	both.BodyTemplate = "{}"
	if err := both.Validate(); err == nil {
		t.Error("multipart + body_template accepted")
	}

	empty := base
	empty.Multipart = &MultipartSpec{}
	if err := empty.Validate(); err == nil {
		t.Error("empty multipart accepted")
	}

	dup := base
	dup.Multipart = &MultipartSpec{
		Fields: map[string]string{"file": "x"},
		Files:  map[string]string{"file": "{{.in.path}}"},
	}
	if err := dup.Validate(); err == nil {
		t.Error("duplicate part name accepted")
	}
}

func TestBuildMultipartSetsFileContentType(t *testing.T) {
	root := withSandbox(t)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	cases := map[string]string{
		"photo.png": "image/png",
		"doc.pdf":   "application/pdf",
		"noext":     "image/png", // sniffed from the bytes
	}
	for name, want := range cases {
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, png, 0o644); err != nil {
			t.Fatal(err)
		}
		body, ct, err := buildMultipart(&MultipartSpec{Files: map[string]string{"file": "{{.in.path}}"}}, nil, map[string]string{"path": file})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		req := httptest.NewRequest("POST", "/", body)
		req.Header.Set("Content-Type", ct)
		if err := req.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, h, err := req.FormFile("file")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := h.Header.Get("Content-Type"); got != want {
			t.Errorf("%s: Content-Type = %q, want %q", name, got, want)
		}
	}
}
