package custom

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/appname"
)

// maxMultipartFileBytes caps one file sent through a multipart recipe.
// The whole body is buffered in memory, so keep it to something the
// daemon absorbs without trouble.
const maxMultipartFileBytes = 64 << 20

// multipartSandboxRoot is the only tree a multipart file may be read
// from: wick's agents dir (session, project and workspace files). A
// wider root would let a model be talked into posting ~/.ssh keys or a
// service .env to an arbitrary API. Indirected so tests can point it at
// a temp dir.
var multipartSandboxRoot = appname.AgentsDir

// buildMultipart renders spec against cfg/in and returns the body plus
// its Content-Type (with boundary). Parts are written in name order so
// the body is deterministic.
func buildMultipart(spec *MultipartSpec, cfg, in map[string]string) (*bytes.Buffer, string, error) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)

	for _, name := range sortedFieldNames(spec.Fields) {
		v, err := renderTemplate("multipart field "+name, spec.Fields[name], cfg, in)
		if err != nil {
			return nil, "", err
		}
		if v == "" {
			continue
		}
		if err := w.WriteField(name, v); err != nil {
			return nil, "", fmt.Errorf("multipart field %s: %w", name, err)
		}
	}

	files := 0
	for _, name := range sortedFieldNames(spec.Files) {
		p, err := renderTemplate("multipart file "+name, spec.Files[name], cfg, in)
		if err != nil {
			return nil, "", err
		}
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		data, base, err := readSandboxedFile(p)
		if err != nil {
			return nil, "", fmt.Errorf("multipart file %s: %w", name, err)
		}
		part, err := w.CreateFormFile(name, base)
		if err != nil {
			return nil, "", fmt.Errorf("multipart file %s: %w", name, err)
		}
		if _, err := part.Write(data); err != nil {
			return nil, "", fmt.Errorf("multipart file %s: %w", name, err)
		}
		files++
	}
	if len(spec.Files) > 0 && files == 0 {
		return nil, "", fmt.Errorf("multipart: no file path given — pass the absolute path of a file inside %s", multipartSandboxRoot())
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("multipart: %w", err)
	}
	return buf, w.FormDataContentType(), nil
}

// readSandboxedFile reads one regular file that must sit inside
// multipartSandboxRoot. Validation and read go through one open handle,
// so the file that passed the check is the file that gets read.
func readSandboxedFile(path string) ([]byte, string, error) {
	root := multipartSandboxRoot()
	if !filepath.IsAbs(path) {
		return nil, "", fmt.Errorf("path must be absolute (got %q) — e.g. %s/projects/<project-id>/files/report.pdf", path, root)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	} else {
		root = filepath.Clean(root)
	}
	abs, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return nil, "", fmt.Errorf("path: %w", err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("path %q is outside %s — files are only read from wick's agents dir", path, root)
	}
	before, err := os.Lstat(abs)
	if err != nil {
		return nil, "", fmt.Errorf("path: %w", err)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, "", fmt.Errorf("path: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("path: %w", err)
	}
	if !os.SameFile(before, st) {
		return nil, "", fmt.Errorf("path %q changed while it was being validated", path)
	}
	if !st.Mode().IsRegular() {
		return nil, "", fmt.Errorf("path %q is not a regular file", path)
	}
	if st.Size() > maxMultipartFileBytes {
		return nil, "", fmt.Errorf("path %q is %d bytes, over the %d-byte limit", path, st.Size(), maxMultipartFileBytes)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxMultipartFileBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read path: %w", err)
	}
	if len(b) > maxMultipartFileBytes {
		return nil, "", fmt.Errorf("path %q grew past the %d-byte limit while being read", path, maxMultipartFileBytes)
	}
	return b, filepath.Base(abs), nil
}

func sortedFieldNames(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
