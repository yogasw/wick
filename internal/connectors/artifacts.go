package connectors

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/pkg/connector"
)

// Files an op returns under connector.FileOutputKey land in
// <session cwd>/artifacts/. The session → cwd mapping lives in the agents
// package, which registers it at boot.

var (
	artifactDirMu       sync.RWMutex
	artifactDirResolver func(sessionID string) (string, error)
)

// SetArtifactDirResolver registers how to find an agent session's working
// directory. nil disables file outputs.
func SetArtifactDirResolver(fn func(sessionID string) (string, error)) {
	artifactDirMu.Lock()
	artifactDirResolver = fn
	artifactDirMu.Unlock()
}

const maxFileOutputBytes = 60 << 20

var (
	unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

	fileSettingMu sync.RWMutex
	fileSetting   func(key string) string
)

// SetFileSettingLoader registers how to read the file_io.* admin settings
// (configs.KeyFileAllowedFormats, configs.KeyFileMaxBinaryMB). They are
// read on every write, so a change applies without a restart. nil means
// built-in defaults.
func SetFileSettingLoader(fn func(key string) string) {
	fileSettingMu.Lock()
	fileSetting = fn
	fileSettingMu.Unlock()
}

func readFileSetting(key string) string {
	fileSettingMu.RLock()
	fn := fileSetting
	fileSettingMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn(key)
}

// allowedFileFormats is the admin's list without its denied or malformed
// entries, or the built-in list when nothing usable is left. The built-in
// text formats stay allowed either way, so narrowing the list to a few
// media types does not switch off existing CSV/JSON exports.
func allowedFileFormats() map[string]bool {
	set, _, _ := connector.ParseFileFormats(readFileSetting(configs.KeyFileAllowedFormats))
	if len(set) == 0 {
		set, _, _ = connector.ParseFileFormats(connector.DefaultFileFormats)
	}
	for _, f := range []string{"csv", "tsv", "json", "ndjson", "txt"} {
		set[f] = true
	}
	return set
}

func init() {
	// Connectors in this process (Slack read_file) refuse a download over
	// the admin limit before fetching it.
	connector.SetMaxBinaryBytesFunc(maxBinaryOutputBytes)
}

func maxBinaryOutputBytes() int {
	return connector.ParseMaxBinaryMB(readFileSetting(configs.KeyFileMaxBinaryMB)) << 20
}

// extractFileOutput writes the file an op attached under FileOutputKey (in
// the already-masked response) to the session's artifacts/ folder and
// returns the response with a FileRef in its place. A response without a
// file is returned unchanged.
func extractFileOutput(respJSON, sessionID string) (string, error) {
	if !strings.Contains(respJSON, `"`+connector.FileOutputKey+`"`) {
		return respJSON, nil
	}
	dec := json.NewDecoder(strings.NewReader(respJSON))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		return respJSON, nil // not an object: nothing to extract
	}
	raw, ok := top[connector.FileOutputKey]
	if !ok {
		return respJSON, nil
	}
	b, _ := json.Marshal(raw)
	var f connector.FileOutput
	if err := json.Unmarshal(b, &f); err != nil {
		return "", fmt.Errorf("file output is malformed: %w", err)
	}
	ref, err := writeArtifact(sessionID, f)
	if err != nil {
		return "", err
	}
	delete(top, connector.FileOutputKey)
	top[connector.FileRefKey] = ref
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(top); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// noSessionHint is the way out offered when a file cannot be written for
// lack of an agent session (a workflow, a schedule, a stdio MCP client).
const noSessionHint = "Run it from an agent session, or ask for less: e.g. Slack read_file returns files up to 2 MB (text 256 KB) inline."

func writeArtifact(sessionID string, f connector.FileOutput) (connector.FileRef, error) {
	format := strings.ToLower(strings.TrimSpace(f.Format))
	if connector.FileFormatDenied(format) {
		return connector.FileRef{}, fmt.Errorf("file format %q is a script or executable and is never written", format)
	}
	if !allowedFileFormats()[format] {
		return connector.FileRef{}, fmt.Errorf("file format %q is not allowed on this wick (admin setting %s)", f.Format, configs.KeyFileAllowedFormats)
	}
	builtin, builtinBinary := connector.FileFormat(format)
	payloads := 0
	for _, set := range []bool{f.Content != "", f.ContentBase64 != "", len(f.Data) > 0} {
		if set {
			payloads++
		}
	}
	if payloads > 1 {
		return connector.FileRef{}, errors.New("file output sets more than one of content, content_base64 and data; set exactly one")
	}
	hasBinary := f.HasBinary()
	switch {
	case builtinBinary && !hasBinary:
		return connector.FileRef{}, fmt.Errorf("file format %q is binary: put the bytes in content_base64", format)
	case !builtin && payloads == 0:
		return connector.FileRef{}, fmt.Errorf("file output for %q is empty", format)
	}
	// A built-in text format may be empty (an export with no rows), and
	// may come as bytes (a CSV that is not UTF-8), written unmasked.
	if len(f.Content) > maxFileOutputBytes {
		return connector.FileRef{}, fmt.Errorf("file is %d MB, over the %d MB limit; narrow the query", len(f.Content)>>20, maxFileOutputBytes>>20)
	}
	maxBinary := maxBinaryOutputBytes()
	if len(f.Data) > maxBinary {
		return connector.FileRef{}, fmt.Errorf("file is %d MB, over the admin limit of %d MB for binary files", len(f.Data)>>20, maxBinary>>20)
	}
	// Reject an oversized blob from its encoded length, before spending
	// memory on decoding it. Whitespace (a wrapped blob) does not count.
	if n := base64.StdEncoding.DecodedLen(base64Len(f.ContentBase64)); n > maxBinary+3 {
		return connector.FileRef{}, fmt.Errorf("file is about %d MB, over the admin limit of %d MB for binary files", n>>20, maxBinary>>20)
	}
	content := []byte(f.Content)
	switch {
	case len(f.Data) > 0:
		content = f.Data
	case f.ContentBase64 != "":
		b, err := decodeFileBase64(f.ContentBase64)
		if err != nil {
			return connector.FileRef{}, err
		}
		if len(b) > maxBinary {
			return connector.FileRef{}, fmt.Errorf("file is %d MB, over the admin limit of %d MB for binary files", len(b)>>20, maxBinary>>20)
		}
		content = b
	}
	switch {
	case !builtin:
		if err := checkSniffedFormat(format, content); err != nil {
			return connector.FileRef{}, err
		}
	case builtinBinary:
		// A page saved as .png would still render as a page wherever the
		// type is sniffed; nothing legitimate looks like HTML here.
		if mt, _, _ := strings.Cut(http.DetectContentType(content), ";"); mt == "text/html" {
			return connector.FileRef{}, fmt.Errorf("file content looks like text/html, not .%s; refusing to save it", format)
		}
	}
	if strings.TrimSpace(sessionID) == "" {
		return connector.FileRef{}, errors.New("this op writes a file into the agent session's artifacts/ folder, and this call has no agent session. " + noSessionHint)
	}
	artifactDirMu.RLock()
	resolve := artifactDirResolver
	artifactDirMu.RUnlock()
	if resolve == nil {
		return connector.FileRef{}, errors.New("this op writes a file into an agent session's artifacts/ folder, which this wick process cannot do (no agent sessions here). " + noSessionHint)
	}
	cwd, err := resolve(sessionID)
	if err != nil || cwd == "" {
		return connector.FileRef{}, fmt.Errorf("cannot find the working directory of session %s: %v", sessionID, err)
	}

	// Every path step goes through os.Root: a symlink swapped in for
	// artifacts/ (or anything under it) cannot lead the write outside the
	// session folder, however the swap is timed.
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return connector.FileRef{}, fmt.Errorf("open session folder: %w", err)
	}
	defer root.Close()
	if err := root.Mkdir("artifacts", 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return connector.FileRef{}, fmt.Errorf("create artifacts folder: %w", err)
	}
	// A symlink planted at artifacts/ is refused outright, even one that
	// stays inside the session folder.
	if fi, err := root.Lstat("artifacts"); err != nil {
		return connector.FileRef{}, fmt.Errorf("check artifacts folder: %w", err)
	} else if !fi.Mode().IsDir() {
		return connector.FileRef{}, errors.New("artifacts/ in the session folder is not a plain directory (a symlink?); refusing to write there")
	}
	dir, err := root.OpenRoot("artifacts")
	if err != nil {
		return connector.FileRef{}, fmt.Errorf("open artifacts folder: %w", err)
	}
	defer dir.Close()

	base := filepath.Base(strings.TrimSpace(f.Name))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.Trim(unsafeNameRe.ReplaceAllString(base, "-"), "-.")
	if len(base) > 80 {
		base = strings.Trim(base[:80], "-.")
	}
	if base == "" {
		base = "export"
	}
	name := base + "." + format
	for i := 2; ; i++ {
		// 0600: artifacts belong to the session's own user (the agent and
		// wick run as that user); other local accounts have no business
		// reading a downloaded private file.
		fh, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			if i > 999 {
				return connector.FileRef{}, errors.New("too many files with the same name in artifacts/")
			}
			name = base + "-" + strconv.Itoa(i) + "." + format
			continue
		}
		if err != nil {
			return connector.FileRef{}, fmt.Errorf("create %s: %w", name, err)
		}
		_, werr := artifactWrite(fh, content)
		cerr := fh.Close()
		if werr != nil || cerr != nil {
			_ = dir.Remove(name)
			return connector.FileRef{}, fmt.Errorf("write %s: %v", name, errors.Join(werr, cerr))
		}
		break
	}
	return connector.FileRef{Path: "artifacts/" + name, Format: format, Bytes: len(content)}, nil
}

// base64Len is the length of s without whitespace.
func base64Len(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\n', '\r', '\t':
		default:
			n++
		}
	}
	return n
}

// takeBinaryFile writes a binary file an in-process op returned (Data or
// ContentBase64) straight from the op's value, before the result is
// marshalled and masked: the bytes are never copied into JSON, never run
// through the masking sweep (which would only corrupt them), and the
// response carries the FileRef. Text content and anything from a plugin
// (already JSON) keep the marshal → mask → extract path.
func takeBinaryFile(value any, sessionID string) (any, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	var f connector.FileOutput
	switch v := m[connector.FileOutputKey].(type) {
	case connector.FileOutput:
		f = v
	case *connector.FileOutput:
		if v == nil {
			return value, nil
		}
		f = *v
	default:
		return value, nil
	}
	if !f.HasBinary() {
		return value, nil
	}
	ref, err := writeArtifact(sessionID, f)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if k != connector.FileOutputKey {
			out[k] = v
		}
	}
	out[connector.FileRefKey] = ref
	return out, nil
}

// sniffedFormats maps a type http.DetectContentType recognises to the
// extensions that type may be saved under.
var sniffedFormats = map[string][]string{
	"image/png": {"png"}, "image/jpeg": {"jpg", "jpeg"}, "image/gif": {"gif"},
	"image/webp": {"webp"}, "image/bmp": {"bmp"}, "image/x-icon": {"ico"},
	"application/pdf": {"pdf"}, "video/mp4": {"mp4", "m4v", "m4a"}, "video/webm": {"webm", "mkv"},
	"audio/mpeg": {"mp3"}, "application/ogg": {"ogg", "oga", "ogv", "opus"},
	"audio/wave": {"wav"}, "audio/aiff": {"aif", "aiff"}, "audio/midi": {"mid", "midi"},
	"video/avi": {"avi"}, "application/x-gzip": {"gz", "tgz"}, "application/x-rar-compressed": {"rar"},
	"application/wasm": {"wasm"}, "application/postscript": {"ps", "eps"},
	"text/html": {"html", "htm"},
	"font/woff": {"woff"}, "font/woff2": {"woff2"}, "font/ttf": {"ttf"}, "font/otf": {"otf"},
	"application/vnd.ms-fontobject": {"eot"},
}

// checkSniffedFormat applies to admin-added formats: when the content is
// a type the sniffer recognises, the extension must be one of that type's.
// Content the sniffer cannot place (plain text, a zip-based office file,
// octet-stream) is accepted on its extension alone.
func checkSniffedFormat(format string, content []byte) error {
	mt, _, _ := strings.Cut(http.DetectContentType(content), ";")
	exts, known := sniffedFormats[mt]
	if !known || slices.Contains(exts, format) {
		return nil
	}
	return fmt.Errorf("file content looks like %s, not .%s; refusing to save it under that extension", mt, format)
}

// artifactWrite writes the file body; a test swaps it to fail mid-write.
var artifactWrite = func(fh *os.File, b []byte) (int, error) { return fh.Write(b) }

// decodeFileBase64 accepts standard and URL-safe base64, padded or not,
// with any line breaks a wrapped blob carries.
func decodeFileBase64(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("file output content_base64 is not valid base64")
}
