package agents

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/pkg/tool"
)

// Upload limits. Multipart parser caps total size at maxMultipartTotal;
// per-file size and count are enforced after parsing.
const (
	maxFilesPerMessage = 5
	maxFileSize        = 25 << 20  // 25 MiB
	maxMultipartTotal  = 150 << 20 // 150 MiB headroom over 5×25
)

// uploadsDirName is the subfolder under <SessionDir>/ where uploads
// live. Kept distinct from "cwd" so file listing endpoints don't mix
// user-uploaded blobs with workspace files.
const uploadsDirName = "uploads"

// saveUploadsFromMultipart writes every "files" form file into the
// session's uploads dir and returns the metadata slice. The caller is
// expected to have already invoked c.R.ParseMultipartForm.
//
// On error any partially-written files are removed so a failed send
// leaves no garbage on disk.
func saveUploadsFromMultipart(c *tool.Ctx, sessionID, baseURL string) ([]store.Attachment, error) {
	form := c.R.MultipartForm
	if form == nil {
		return nil, nil
	}
	files := form.File["files"]
	if len(files) == 0 {
		return nil, nil
	}
	if len(files) > maxFilesPerMessage {
		return nil, fmt.Errorf("too many files (max %d)", maxFilesPerMessage)
	}

	dir := filepath.Join(globalLayout.SessionDir(sessionID), uploadsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir uploads: %w", err)
	}

	out := make([]store.Attachment, 0, len(files))
	written := make([]string, 0, len(files))
	cleanup := func() {
		for _, p := range written {
			_ = os.Remove(p)
		}
	}

	for _, fh := range files {
		if fh.Size > maxFileSize {
			cleanup()
			return nil, fmt.Errorf("file %q exceeds %d MiB", fh.Filename, maxFileSize>>20)
		}
		stored, err := buildStoredName(fh.Filename)
		if err != nil {
			cleanup()
			return nil, err
		}
		absPath := filepath.Join(dir, stored)
		if err := writeUploadedFile(fh, absPath); err != nil {
			cleanup()
			return nil, err
		}
		written = append(written, absPath)

		// Trust extension-derived MIME only — the client-supplied
		// Content-Type in the multipart part header is attacker-controlled
		// and would let an .html upload masquerade as image/png.
		mt := normalizeExtMIME(filepath.Ext(stored))
		out = append(out, store.Attachment{
			Name:       fh.Filename,
			StoredName: stored,
			URL:        strings.TrimRight(baseURL, "/") + "/sessions/" + sessionID + "/uploads/" + stored,
			AbsPath:    absPath,
			MIME:       mt,
			Size:       fh.Size,
		})
	}
	return out, nil
}

// writeUploadedFile streams a multipart.FileHeader to disk.
func writeUploadedFile(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return fmt.Errorf("open upload %q: %w", fh.Filename, err)
	}
	defer func() { _ = src.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create upload %q: %w", dst, err)
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("write upload %q: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("close upload %q: %w", dst, err)
	}
	return nil
}

// buildStoredName returns a fully-opaque on-disk filename for an upload:
// 32 hex chars from crypto/rand plus a sanitized extension. The
// extension is kept so MIME / Content-Type detection on serve still
// works, but the user-supplied basename is dropped — the original is
// kept in store.Attachment.Name for display only.
//
// Rationale: an opaque random ID means an attacker who knows the
// session ID still cannot enumerate uploads by guessing common
// filenames (e.g. "screenshot.png", "log.txt"). Combined with the
// session ID being a UUID, the full URL has ~256 bits of entropy.
func buildStoredName(orig string) (string, error) {
	ext := sanitizeFilenamePart(strings.TrimPrefix(filepath.Ext(orig), "."))
	if len(ext) > 12 {
		ext = ext[:12]
	}
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return "", fmt.Errorf("random id: %w", err)
	}
	stored := hex.EncodeToString(idBytes[:])
	if ext != "" {
		stored += "." + ext
	}
	return stored, nil
}

// sanitizeFilenamePart keeps ASCII alnum, dash, underscore. Everything
// else (including dots, slashes, control chars, unicode) becomes "_".
func sanitizeFilenamePart(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			unicode.IsDigit(r),
			r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// imageMIMEAllowed is the subset of inline-safe types that the UI
// renders as <img> thumbnails in the user bubble. SVG is intentionally
// excluded — it can carry <script>; if image previews matter for SVG,
// rasterize server-side first.
var imageMIMEAllowed = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/avif": true,
}

// inlineSafeMIME is the full whitelist of types served inline (image
// thumbnails + plain-text previews opened in a new tab). Anything not
// in this map is forced to download via Content-Disposition: attachment
// so a malicious upload can't execute in the user's session.
//
// Plain-text types are served with an explicit "; charset=utf-8" and
// text/plain Content-Type so browsers cannot interpret them as
// markdown/HTML/JS — `.md` shows as raw source, not rendered HTML.
var inlineSafeMIME = map[string]string{
	"image/png":              "image/png",
	"image/jpeg":             "image/jpeg",
	"image/gif":              "image/gif",
	"image/webp":             "image/webp",
	"image/avif":             "image/avif",
	"application/pdf":        "application/pdf",
	"text/plain":             "text/plain; charset=utf-8",
	"text/markdown":          "text/plain; charset=utf-8",
	"text/csv":               "text/plain; charset=utf-8",
	"text/x-log":             "text/plain; charset=utf-8",
	"application/json":       "application/json; charset=utf-8",
	"application/x-yaml":     "text/plain; charset=utf-8",
	"application/yaml":       "text/plain; charset=utf-8",
	"text/yaml":              "text/plain; charset=utf-8",
	"text/x-go":              "text/plain; charset=utf-8",
	"text/x-python":          "text/plain; charset=utf-8",
	"text/x-shellscript":     "text/plain; charset=utf-8",
	"application/javascript": "text/plain; charset=utf-8",
	"text/javascript":        "text/plain; charset=utf-8",
	"text/x-toml":            "text/plain; charset=utf-8",
}

// sessionUploadServe streams a previously-uploaded file back to the
// browser. Path:  GET /sessions/{id}/uploads/{name}.
//
// Hardening:
//   - Filename pattern validator rejects traversal / weird chars.
//   - Auth: route lives under /tools/agents/* — wrapped by
//     authMidd.RequireToolAccess at server boot; anonymous callers
//     cannot reach this handler.
//   - X-Content-Type-Options: nosniff disables browser MIME sniffing.
//   - Inline rendering is restricted to a strict image-MIME whitelist
//     by file extension. Every other shape is served as a download so
//     uploaded HTML / SVG / scripts cannot execute against the wick
//     origin (would be DOM-XSS otherwise).
//   - Content-Type is set explicitly from the whitelist — the file's
//     own bytes are NOT trusted to pick the type.
func sessionUploadServe(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	name := c.PathValue("name")
	if !isSafeStoredName(name) {
		c.Error(http.StatusBadRequest, "invalid upload name")
		return
	}
	if _, ok := globalMgr.Registry().Session(id); !ok {
		c.Error(http.StatusNotFound, "session not found")
		return
	}
	full := filepath.Join(globalLayout.SessionDir(id), uploadsDirName, name)
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		c.Error(http.StatusNotFound, "not found")
		return
	}

	ct := normalizeExtMIME(filepath.Ext(name))
	serveCT, inline := inlineSafeMIME[ct]

	c.W.Header().Set("X-Content-Type-Options", "nosniff")
	c.W.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if inline {
		c.W.Header().Set("Content-Type", serveCT)
		c.W.Header().Set("Content-Disposition", `inline; filename="`+sanitizeHeaderFilename(name)+`"`)
	} else {
		c.W.Header().Set("Content-Type", "application/octet-stream")
		c.W.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeHeaderFilename(name)+`"`)
	}
	f, err := os.Open(full)
	if err != nil {
		c.Error(http.StatusInternalServerError, "open file")
		return
	}
	defer func() { _ = f.Close() }()
	http.ServeContent(c.W, c.R, name, info.ModTime(), f)
}

// sanitizeHeaderFilename strips chars that break the Content-Disposition
// header (CR/LF/quote/backslash). Stored names are already safe but
// belt-and-suspenders since the value is interpolated into a header.
func sanitizeHeaderFilename(s string) string {
	return strings.NewReplacer("\r", "", "\n", "", `"`, "", `\`, "").Replace(s)
}

// extMIMEOverrides covers dev-centric extensions Go's mime package
// doesn't know (or maps to something less useful). All values must be
// keys in inlineSafeMIME for the file to render inline.
var extMIMEOverrides = map[string]string{
	".md":         "text/markdown",
	".markdown":   "text/markdown",
	".log":        "text/x-log",
	".yaml":       "application/yaml",
	".yml":        "application/yaml",
	".toml":       "text/x-toml",
	".go":         "text/x-go",
	".py":         "text/x-python",
	".sh":         "text/x-shellscript",
	".bash":       "text/x-shellscript",
	".js":         "text/javascript",
	".mjs":        "text/javascript",
	".cjs":        "text/javascript",
	".ts":         "text/plain",
	".tsx":        "text/plain",
	".jsx":        "text/plain",
	".env":        "text/plain",
	".ini":        "text/plain",
	".conf":       "text/plain",
	".cfg":        "text/plain",
	".csv":        "text/csv",
	".jsonl":      "application/json",
	".ndjson":     "application/json",
}

// normalizeExtMIME returns the canonical lower-case MIME for a file
// extension, stripping any "; charset=…" suffix. Overrides win over
// Go's built-in mime.TypeByExtension table.
func normalizeExtMIME(ext string) string {
	ext = strings.ToLower(ext)
	if v, ok := extMIMEOverrides[ext]; ok {
		return v
	}
	mt := mime.TypeByExtension(ext)
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = mt[:i]
	}
	return strings.TrimSpace(strings.ToLower(mt))
}

// isSafeStoredName matches the buildStoredName output shape — digits,
// hex, alnum/dash/underscore, optional ext. Rejects any path separators
// or "..".
func isSafeStoredName(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	if strings.ContainsAny(s, `/\`) || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.':
			continue
		default:
			return false
		}
	}
	return true
}
